package tokenkit

import (
	"unicode"
	"unicode/utf8"
)

// The pre-tokenizers below split text exactly as the split patterns of
// tiktoken-go v0.8.1 do. tiktoken-go runs those patterns through matchers that
// regexp2cg generated (codec/regexp.gen.go), and two behaviors there differ
// from a textbook reading of the patterns; both are reproduced here:
//   - The ASCII bitmaps of [^\r\n\p{L}\p{N}] and [^\s\p{L}\p{N}] leave out
//     U+007F, so no branch matches it and the matcher drops it.
//   - \s*[\r\n]+ backtracks to the first line break of a whitespace run
//     instead of the last.
//
// Both pre-tokenizers expect valid UTF-8: regexp2 reads each invalid byte as
// U+FFFD, which they do not model.

// Rune classes of the split patterns, as the generated matchers test them.
const (
	classUpper  = 1 << iota // [\p{Lu}\p{Lt}\p{Lm}\p{Lo}\p{M}]
	classLower              // [\p{Ll}\p{Lm}\p{Lo}\p{M}]
	classLetter             // \p{L}
	classNumber             // \p{N}
	classSpace              // \s, which regexp2 tests with unicode.IsSpace
	classPrefix             // [^\r\n\p{L}\p{N}]
	classSymbol             // [^\s\p{L}\p{N}]
)

// categoryClasses gives the classes of the runes in each unicode table the
// generated matchers test. The tables do not overlap, and a rune in none of
// them is in classPrefix and classSymbol. unicode.IsSpace reports White_Space.
var categoryClasses = [...]struct {
	table *unicode.RangeTable
	class uint8
}{
	{unicode.Lo, classUpper | classLower | classLetter},
	{unicode.Lm, classUpper | classLower | classLetter},
	{unicode.Ll, classLower | classLetter},
	{unicode.Lu, classUpper | classLetter},
	{unicode.Lt, classUpper | classLetter},
	{unicode.M, classUpper | classLower | classPrefix | classSymbol},
	{unicode.N, classNumber},
	{unicode.White_Space, classSpace | classPrefix},
}

// bmpClasses holds the classes of the runes of the Basic Multilingual Plane.
var bmpClasses = func() (classes [1 << 16]uint8) {
	for r := range classes {
		classes[r] = classPrefix | classSymbol
	}
	for _, c := range categoryClasses {
		for _, rg := range c.table.R16 {
			for r := int(rg.Lo); r <= int(rg.Hi); r += int(rg.Stride) {
				classes[r] = c.class
			}
		}
	}
	// For ASCII the generated matchers test [^\r\n\p{L}\p{N}] and
	// [^\s\p{L}\p{N}] against these bitmaps of the low and high 64 runes.
	const (
		prefixLow, prefixHigh = 0xfc00ffffffffdbff, 0x78000001f8000001
		symbolLow, symbolHigh = 0xfc00fffeffffc1ff, 0x78000001f8000001
	)
	for r := range utf8.RuneSelf {
		prefix, symbol, bit := uint64(prefixLow), uint64(symbolLow), uint(r)
		if r >= 64 {
			prefix, symbol, bit = prefixHigh, symbolHigh, bit-64
		}
		class := classes[r] &^ (classPrefix | classSymbol)
		if prefix&(1<<bit) != 0 {
			class |= classPrefix
		}
		if symbol&(1<<bit) != 0 {
			class |= classSymbol
		}
		classes[r] = class
	}
	return classes
}()

// runEnd returns where the run of runes in any of classes that starts at i
// ends. ASCII, the common case, is classified without a call.
func runEnd(text string, i int, classes uint8) int {
	for i < len(text) {
		if c := text[i]; c < utf8.RuneSelf {
			if bmpClasses[c]&classes == 0 {
				break
			}
			i++
			continue
		}
		class, size := unicodeClassAt(text, i)
		if class&classes == 0 {
			break
		}
		i += size
	}
	return i
}

func classAt(text string, i int) (class uint8, size int) {
	if c := text[i]; c < utf8.RuneSelf {
		return bmpClasses[c], 1
	}
	return unicodeClassAt(text, i)
}

func unicodeClassAt(text string, i int) (class uint8, size int) {
	r, size := utf8.DecodeRuneInString(text[i:])
	if r < 1<<16 {
		return bmpClasses[r], size
	}
	for _, c := range categoryClasses {
		if unicode.Is(c.table, r) {
			return c.class, size
		}
	}
	return classPrefix | classSymbol, size
}

// nextO200kPiece returns the first piece at or after pos that the o200k_base
// split pattern matches, or start == len(text) when none is left:
//
//	[^\r\n\p{L}\p{N}]?[\p{Lu}\p{Lt}\p{Lm}\p{Lo}\p{M}]*[\p{Ll}\p{Lm}\p{Lo}\p{M}]+(?i:'s|'t|'re|'ve|'m|'ll|'d)?|
//	[^\r\n\p{L}\p{N}]?[\p{Lu}\p{Lt}\p{Lm}\p{Lo}\p{M}]+[\p{Ll}\p{Lm}\p{Lo}\p{M}]*(?i:'s|'t|'re|'ve|'m|'ll|'d)?|
//	\p{N}{1,3}| ?[^\s\p{L}\p{N}]+[\r\n/]*|\s*[\r\n]+|\s+(?!\S)|\s+
func nextO200kPiece(text string, pos int) (start, end int) {
	for pos < len(text) {
		class, size := classAt(text, pos)
		if class&(classPrefix|classUpper|classLower) != 0 {
			// Each word branch tries its optional prefix first, then none;
			// the first branch that matches wins.
			var prefixed, bare [2]int
			if class&classPrefix != 0 {
				prefixed = o200kWordEnds(text, pos+size)
			}
			if class&(classUpper|classLower) != 0 {
				bare = o200kWordEnds(text, pos)
			}
			for _, end := range [...]int{prefixed[0], bare[0], prefixed[1], bare[1]} {
				if end > 0 {
					return pos, contractionEnd(text, end)
				}
			}
		}
		if class&classNumber != 0 {
			return pos, numbersEnd(text, pos+size)
		}
		if end := symbolsEnd(text, pos, true); end > pos {
			return pos, end
		}
		if end := spacesEnd(text, pos); end > pos {
			return pos, end
		}
		// No branch matches here (only U+007F); the matcher moves on.
		pos += size
	}
	return len(text), len(text)
}

// o200kWordEnds returns where the two word branches of o200k_base end when
// their letters start at i, before any contraction, or 0 for a branch that does
// not match there:
//
//	[\p{Lu}\p{Lt}\p{Lm}\p{Lo}\p{M}]*[\p{Ll}\p{Lm}\p{Lo}\p{M}]+
//	[\p{Lu}\p{Lt}\p{Lm}\p{Lo}\p{M}]+[\p{Ll}\p{Lm}\p{Lo}\p{M}]*
func o200kWordEnds(text string, i int) (ends [2]int) {
	start, lastLower := i, 0
	for i < len(text) {
		class, size := classAt(text, i)
		if class&classUpper == 0 {
			break
		}
		i += size
		if class&classLower != 0 {
			lastLower = i
		}
	}
	upperEnd := i
	i = runEnd(text, i, classLower)
	// When no lowercase rune follows the greedy uppercase run, the first
	// branch backtracks into the run up to its last rune that is in both sets.
	switch {
	case i > upperEnd:
		ends[0] = i
	case lastLower > 0:
		ends[0] = lastLower
	}
	if upperEnd > start {
		ends[1] = i
	}
	return ends
}

// nextCl100kPiece returns the first piece at or after pos that the cl100k_base
// split pattern matches, or start == len(text) when none is left:
//
//	(?i:'s|'t|'re|'ve|'m|'ll|'d)|[^\r\n\p{L}\p{N}]?\p{L}+|\p{N}{1,3}|
//	 ?[^\s\p{L}\p{N}]+[\r\n]*|\s*[\r\n]+|\s+(?!\S)|\s+
func nextCl100kPiece(text string, pos int) (start, end int) {
	for pos < len(text) {
		if end := contractionEnd(text, pos); end > pos {
			return pos, end
		}
		class, size := classAt(text, pos)
		// A rune that can be the prefix is not a letter itself.
		letters := pos
		if class&classPrefix != 0 {
			letters += size
		}
		if end := runEnd(text, letters, classLetter); end > letters {
			return pos, end
		}
		if class&classNumber != 0 {
			return pos, numbersEnd(text, pos+size)
		}
		if end := symbolsEnd(text, pos, false); end > pos {
			return pos, end
		}
		if end := spacesEnd(text, pos); end > pos {
			return pos, end
		}
		// No branch matches here (only U+007F); the matcher moves on.
		pos += size
	}
	return len(text), len(text)
}

// contractionEnd extends a match ending at i over (?i:'s|'t|'re|'ve|'m|'ll|'d).
// Case folding lets 's also match U+017F (ſ), encoded as C5 BF.
func contractionEnd(text string, i int) int {
	if i+1 >= len(text) || text[i] != '\'' {
		return i
	}
	switch text[i+1] {
	case 'S', 's', 'T', 't', 'M', 'm', 'D', 'd':
		return i + 2
	case 'R', 'r', 'V', 'v':
		if i+2 < len(text) && text[i+2]|0x20 == 'e' {
			return i + 3
		}
	case 'L', 'l':
		if i+2 < len(text) && text[i+2]|0x20 == 'l' {
			return i + 3
		}
	case 0xC5:
		if i+2 < len(text) && text[i+2] == 0xBF {
			return i + 3
		}
	}
	return i
}

// numbersEnd ends \p{N}{1,3} whose first rune ends at i.
func numbersEnd(text string, i int) int {
	for range 2 {
		if i == len(text) {
			break
		}
		class, size := classAt(text, i)
		if class&classNumber == 0 {
			break
		}
		i += size
	}
	return i
}

// symbolsEnd matches ` ?[^\s\p{L}\p{N}]+[\r\n]*` at i, with `/` also allowed
// after the symbols when slash is set. It returns i when nothing matches.
func symbolsEnd(text string, i int, slash bool) int {
	start := i
	if text[i] == ' ' {
		i++
	}
	symbols := i
	if i = runEnd(text, i, classSymbol); i == symbols {
		return start
	}
	for i < len(text) && (text[i] == '\r' || text[i] == '\n' || slash && text[i] == '/') {
		i++
	}
	return i
}

// spacesEnd matches `\s*[\r\n]+|\s+(?!\S)|\s+` at i. It returns i when text[i]
// is not whitespace.
func spacesEnd(text string, i int) int {
	start, last := i, i
	for i < len(text) {
		// \s*[\r\n]+ ends with the first run of line breaks (see above).
		if c := text[i]; c == '\r' || c == '\n' {
			for i < len(text) && (text[i] == '\r' || text[i] == '\n') {
				i++
			}
			return i
		}
		class, size := classAt(text, i)
		if class&classSpace == 0 {
			break
		}
		last = i
		i += size
	}
	// Before other text, a run of two or more leaves its last rune to the
	// next piece.
	if i < len(text) && last > start {
		return last
	}
	return i
}
