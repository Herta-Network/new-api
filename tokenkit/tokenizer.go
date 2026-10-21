package tokenkit

import (
	"hash/maphash"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/tiktoken-go/tokenizer"
	"github.com/tiktoken-go/tokenizer/codec"
)

var (
	o200kBase  = sync.OnceValue(func() *encoding { return newEncoding(codec.NewO200kBase(), nextO200kPiece) })
	cl100kBase = sync.OnceValue(func() *encoding { return newEncoding(codec.NewCl100kBase(), nextCl100kPiece) })
)

// maxRunBytes bounds a run of letters, whitespace, or symbols passed to the
// encoder in one piece. BPE cost grows with the square of a pre-tokenized
// piece: 100,000 Chinese characters without punctuation take about 20 s in one
// piece. Cutting longer runs keeps counting linear; ordinary text has no such
// runs, and a cut changes the count only inside the run (under 0.5% for CJK).
const maxRunBytes = 1024

// maxPieceBytes bounds a piece passed to the encoder at once. A piece can also
// span runs of alternating classes, which maxRunBytes never cuts: symbols with
// combining marks between them, or line breaks and slashes after symbols.
// Text that maxRunBytes does not cut has every run shorter than maxRunBytes,
// and a piece of at most two such runs fits in maxPieceBytes and is counted
// exactly; invalid UTF-8 triples in size when read as U+FFFD, so its runs can
// be cut sooner. Longer pieces are cut every maxPieceBytes.
const maxPieceBytes = 2 * maxRunBytes

// Piece cache bounds. The cache is a set-associative table that never grows:
// a piece may sit in any slot of the set its hash picks, and a new piece takes
// the first empty slot of its set or replaces one the hash picks. An encoding
// keeps at most pieceCacheSlots pieces of up to maxCachedPieceBytes each: about
// 10 MB at worst, and 3 MB once full of agent traffic, where most pieces are a
// few bytes. Longer pieces are rare, rarely repeat, and are counted every time.
const (
	pieceCacheSlots     = 1 << 16
	pieceCacheWays      = 4
	maxCachedPieceBytes = 128
)

// encoding counts tokens exactly as its codec does, but splits text into
// pieces with a hand-written pre-tokenizer instead of regexp2, where the codec
// spends most of its time, and caches the count of each short piece. The codec
// encodes each piece on its own, so the sum over the pieces is its total.
type encoding struct {
	codec     tokenizer.Codec
	nextPiece func(text string, pos int) (start, end int)
	seed      maphash.Seed
	pieces    [pieceCacheSlots]atomic.Pointer[countedPiece]
}

type countedPiece struct {
	piece  string
	tokens int
}

func newEncoding(c tokenizer.Codec, nextPiece func(text string, pos int) (start, end int)) *encoding {
	return &encoding{codec: c, nextPiece: nextPiece, seed: maphash.MakeSeed()}
}

// Count returns the token count of text for model. GPT and o-series models
// are counted with their tiktoken encoding; other models use Estimate.
func Count(model, text string) int {
	if text == "" {
		return 0
	}
	model = strings.ToLower(model)
	if ModelFamily(model) != FamilyOpenAI {
		return Estimate(model, text)
	}
	e := o200kBase()
	if usesCl100k(model) {
		e = cl100kBase()
	}
	return e.countText(text)
}

// countText returns the token count of text.
func (e *encoding) countText(text string) int {
	if !utf8.ValidString(text) {
		return countChunks(text, e.countInvalid)
	}
	return countChunks(text, e.count)
}

// countChunks returns the sum of count over the chunks of text that are
// encoded separately: text is cut inside every letter, whitespace, or symbol
// run that reaches maxRunBytes.
func countChunks(text string, count func(chunk string) int) int {
	total, start := 0, 0
	runClass, runStart := runNone, 0
	for i, r := range text {
		class := runClassOf(r)
		if class != runClass {
			runClass, runStart = class, i
			continue
		}
		if class != runNumber && i-runStart >= maxRunBytes {
			total += count(text[start:i])
			start, runStart = i, i
		}
	}
	return total + count(text[start:])
}

// countInvalid returns the token count of a chunk that is not valid UTF-8,
// which regexp2 reads with U+FFFD for each invalid byte.
func (e *encoding) countInvalid(chunk string) int {
	return e.count(string([]rune(chunk)))
}

// count returns the token count of chunk, which must be valid UTF-8.
func (e *encoding) count(chunk string) int {
	total := 0
	for start, end := e.nextPiece(chunk, 0); start < len(chunk); start, end = e.nextPiece(chunk, end) {
		total += e.pieceCount(chunk[start:end])
	}
	return total
}

// pieceCount returns the token count of one piece, from the cache when the
// piece is short enough to keep, and in parts when it is longer than
// maxPieceBytes.
func (e *encoding) pieceCount(piece string) int {
	if len(piece) > maxCachedPieceBytes {
		total := 0
		for len(piece) > maxPieceBytes {
			cut := maxPieceBytes
			for !utf8.RuneStart(piece[cut]) {
				cut--
			}
			total += e.codecCount(piece[:cut])
			piece = piece[cut:]
		}
		return total + e.codecCount(piece)
	}
	h := maphash.String(e.seed, piece)
	set := (*[pieceCacheWays]atomic.Pointer[countedPiece])(e.pieces[h%pieceCacheSlots&^(pieceCacheWays-1):])
	// Slots fill in order and are never emptied, so an empty slot ends the set.
	slot := &set[(h>>32)%pieceCacheWays]
	for i := range set {
		cached := set[i].Load()
		if cached == nil {
			slot = &set[i]
			break
		}
		if cached.piece == piece {
			return cached.tokens
		}
	}
	tokens := e.codecCount(piece)
	slot.Store(&countedPiece{piece: strings.Clone(piece), tokens: tokens})
	return tokens
}

func (e *encoding) codecCount(text string) int {
	n, _ := e.codec.Count(text)
	return n
}

type runClass int

const (
	runNone runClass = iota
	runLetter
	runNumber
	runSpace
	runSymbol
)

// runClassOf groups runes the way tiktoken's pre-tokenizer joins them into
// pieces. Digits are split into groups of three by the encoder itself.
func runClassOf(r rune) runClass {
	if r < utf8.RuneSelf {
		return asciiRunClasses[r]
	}
	return unicodeRunClass(r)
}

var asciiRunClasses = func() (classes [utf8.RuneSelf]runClass) {
	for r := range rune(utf8.RuneSelf) {
		switch {
		case 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z':
			classes[r] = runLetter
		case '0' <= r && r <= '9':
			classes[r] = runNumber
		case r == ' ' || '\t' <= r && r <= '\r':
			classes[r] = runSpace
		default:
			classes[r] = runSymbol
		}
	}
	return classes
}()

// unicodeRunClass is runClassOf outside ASCII, kept apart so that runClassOf
// inlines.
func unicodeRunClass(r rune) runClass {
	switch {
	case unicode.IsLetter(r) || unicode.IsMark(r):
		return runLetter
	case unicode.IsNumber(r):
		return runNumber
	case unicode.IsSpace(r):
		return runSpace
	default:
		return runSymbol
	}
}

// usesCl100k reports whether an OpenAI model predates o200k_base: GPT-4 (but
// not GPT-4o, GPT-4.1, or GPT-4.5) and GPT-3.5. Every later model, including
// GPT-5, GPT-6, and the o-series, uses o200k_base.
func usesCl100k(model string) bool {
	model = strings.TrimPrefix(model, "ft:")
	return model == "gpt-4" || strings.HasPrefix(model, "gpt-4-") ||
		strings.HasPrefix(model, "gpt-3.5") || strings.HasPrefix(model, "gpt-35")
}
