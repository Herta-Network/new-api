package tokenkit

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dlclark/regexp2/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tiktoken-go/tokenizer"
	"github.com/tiktoken-go/tokenizer/codec"
)

func TestModelFamily(t *testing.T) {
	for _, tc := range []struct {
		model  string
		family Family
	}{
		{"claude-3-5-sonnet-20240620", FamilyClaude},
		{"claude-sonnet-4-20250514", FamilyClaude},
		{"claude-sonnet-4-5-20250929", FamilyClaude},
		{"claude-haiku-4-5", FamilyClaude},
		{"claude-sonnet-4-6", FamilyClaude},
		// Claude Opus 4.7 introduced the tokenizer every later Claude model uses.
		{"claude-opus-4-7", FamilyClaude47},
		{"claude-opus-4-7@20260416", FamilyClaude47},
		{"us.anthropic.claude-opus-4-7-v1:0", FamilyClaude47},
		{"claude-sonnet-5", FamilyClaude47},
		{"claude-fable-5-1", FamilyClaude47},
		{"Claude-Opus-5-5", FamilyClaude47},
		{"claude-mythos-preview", FamilyClaude47},
		{"gemini-3.7-flash", FamilyGemini},
		{"gpt-5.6-sol", FamilyOpenAI},
		{"gpt-6-astra", FamilyOpenAI},
		{"o3-mini", FamilyOpenAI},
		{"deepseek-v4", FamilyUnknown},
	} {
		assert.Equal(t, tc.family, ModelFamily(tc.model), tc.model)
	}
}

func TestEstimateUsesFamilyWeights(t *testing.T) {
	const text = "func main() {\n\tfmt.Println(\"统计 token\", 42)\n}\nSee https://example.com/a?b=1"
	require.NotEqual(t, EstimateFamily(FamilyClaude, text), EstimateFamily(FamilyClaude47, text))

	assert.Equal(t, EstimateFamily(FamilyClaude, text), Estimate("claude-haiku-4-5", text))
	assert.Equal(t, EstimateFamily(FamilyClaude47, text), Estimate("claude-sonnet-5", text))
	assert.Equal(t, EstimateFamily(FamilyUnknown, text), Estimate("deepseek-v4", text))
	assert.Equal(t, EstimateFamily(FamilyUnknown, text), EstimateFamily("other", text))
	assert.Zero(t, Estimate("claude-sonnet-5", ""))
}

func TestCountUsesTiktokenForOpenAIModels(t *testing.T) {
	const text = "大语言模型按 token 计费；Large language models bill by token. 🙂"
	o200k, _ := codec.NewO200kBase().Count(text)
	cl100k, _ := codec.NewCl100kBase().Count(text)
	require.NotEqual(t, o200k, cl100k)

	assert.Equal(t, o200k, Count("gpt-5.6-sol", text))
	assert.Equal(t, o200k, Count("gpt-6-astra", text))
	assert.Equal(t, o200k, Count("gpt-4o-mini", text))
	assert.Equal(t, cl100k, Count("gpt-4", text))
	assert.Equal(t, cl100k, Count("gpt-3.5-turbo", text))
	assert.Equal(t, Estimate("claude-sonnet-5", text), Count("claude-sonnet-5", text))
}

// Long runs without separators are cut before encoding so BPE stays linear;
// ordinary text is counted exactly and cut runs stay within 0.5%.
func TestCountCutsOnlyLongRuns(t *testing.T) {
	encoder := codec.NewO200kBase()
	ordinary := strings.Repeat("func main() {\n\tfmt.Println(\"统计 token，按模型计费。\", 42)\n}\n| a | b |\n|---|---|\n", 200)
	want, _ := encoder.Count(ordinary)
	assert.Equal(t, want, Count("gpt-5.6-sol", ordinary))

	rng := rand.New(rand.NewSource(1))
	var cjk strings.Builder
	for range 5000 {
		cjk.WriteRune(rune(0x4E00 + rng.Intn(0x9FA5-0x4E00)))
	}
	for _, text := range []string{cjk.String(), strings.Repeat("token", 2000), "a" + strings.Repeat(" ", 10000) + "b", strings.Repeat("=", 10000)} {
		exact, _ := encoder.Count(text)
		assert.InEpsilon(t, exact, Count("gpt-5.6-sol", text), 0.005)
	}
}

// splitPatterns are the split patterns of tiktoken-go v0.8.1, keyed by a model
// that uses them. regexp2 runs these exact strings with the matchers that the
// codec package generated and registered, as the codec itself does.
var splitPatterns = map[string]string{
	"gpt-5.6-sol": `[^\r\n\p{L}\p{N}]?[\p{Lu}\p{Lt}\p{Lm}\p{Lo}\p{M}]*[\p{Ll}\p{Lm}\p{Lo}\p{M}]+(?i:'s|'t|'re|'ve|'m|'ll|'d)?|[^\r\n\p{L}\p{N}]?[\p{Lu}\p{Lt}\p{Lm}\p{Lo}\p{M}]+[\p{Ll}\p{Lm}\p{Lo}\p{M}]*(?i:'s|'t|'re|'ve|'m|'ll|'d)?|\p{N}{1,3}| ?[^\s\p{L}\p{N}]+[\r\n/]*|\s*[\r\n]+|\s+(?!\S)|\s+`,
	"gpt-4":       `(?i:'s|'t|'re|'ve|'m|'ll|'d)|[^\r\n\p{L}\p{N}]?\p{L}+|\p{N}{1,3}| ?[^\s\p{L}\p{N}]+[\r\n]*|\s*[\r\n]+|\s+(?!\S)|\s+`,
}

var preTokenizerSamples = []string{
	// Code with tabs, CRLF line ends, indentation, and trailing spaces.
	"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"统计 token\", 42) // 打印\n}\n",
	"def f(x):  \r\n    return x\r\n\r\n\r\nclass A:\n        pass  \n",
	"{\"model\": \"gpt-5.6-sol\", \"messages\": [{\"role\": \"user\", \"content\": \"hi\\n\"}]}\n",
	"if (a && b) {\n    return a->b;\n}\n/* comment */\n// done\n",
	// CJK text and full-width punctuation.
	"大语言模型按 token 计费；输入、输出分开计算。「引号」（括号）！",
	"日本語のテキスト、カタカナ「ー」と々を含む。한국어 문장입니다.",
	// Emoji with skin tones, ZWJ sequences, flags, and keycaps.
	"👍🏽 ok 😀😀 👨\u200d👩\u200d👧 🇨🇳 1\ufe0f\u20e3 #\ufe0f\u20e3",
	// Combining marks alone and after spaces, symbols, digits, and capitals.
	"\u0301AB. e\u0301 \u0301x !\u0301 1\u0301 A\u0301B \u0301A \u0301 नमस्ते ภาษาไทย שָׁלוֹם",
	// Whitespace runs before words, before line breaks, and at the end.
	"a  b   c\t\td \u00a0e \u3000f\u2028g\u0085h\v\fi",
	"x  \n  \n\n   y\r\n\r\n  z   \n\t\n",
	"   leading and trailing   ",
	// Contractions in every case, including U+017F, and capital runs.
	"don't I'M WE'LL they'Re you'VE she'd it'ſ IT'S 'll 'Ve O'Neil rock'n'roll ''s x' 'tis I'dk we'llx",
	"ǅemal ǈ HTTPServer XMLHttpRequest iOS McDonald ΑΒΓαβγ ПРИВЕТмир İstanbul 𝐀𝐁𝐂 𠀀𠀁",
	// Numbers in several scripts.
	"1234567 3.14159 1,000,000 v1.2.3 0x1F ٣٤٥ ⅫⅩ ½ x²³ 𝟎𝟏𝟐",
	// Punctuation followed by line breaks and slashes.
	"}\n\n);\r\n...\n//\n*/\n\n ->\n!\n/x :\n/\n",
	// URLs and paths.
	"see https://github.com/QuantumNous/new-api/blob/main/AGENTS.md?plain=1#L10 or ./tokenkit/tokenizer.go and C:\\Users\\me",
	// Control characters; U+007F matches no branch of the generated matchers.
	"a\x7fb !\x7f! \x7f  \x7f\n \x1b[31mred\x1b[0m \x00\x1c\x1d <|endoftext|>",
	// Runs longer than maxRunBytes.
	strings.Repeat("a", 3000),
	strings.Repeat("统计按模型计费输入输出", 60),
	"a" + strings.Repeat(" ", 3000) + "b",
	strings.Repeat(" \n", 1000),
	strings.Repeat("=", 3000),
	strings.Repeat("😀", 600),
	strings.Repeat("e\u0301", 800),
	strings.Repeat("9", 5000),
	// Invalid UTF-8, which regexp2 reads as U+FFFD for each invalid byte.
	"abc\xff\xfedef",
	"héllo\xc3",
	"\x80 hello \xe4\xb8",
	strings.Repeat("\xff", 600),
}

// The pre-tokenizers cut text where the split patterns of tiktoken-go do.
func TestPreTokenizersMatchRegexp2(t *testing.T) {
	for model, pattern := range splitPatterns {
		e := o200kBase()
		if usesCl100k(model) {
			e = cl100kBase()
		}
		split := regexp2.MustCompile(pattern, regexp2.None)
		for _, text := range preTokenizerSamples {
			if !utf8.ValidString(text) {
				continue
			}
			var want, got [][2]int
			m, err := split.FindStringMatch(text)
			for ; m != nil && err == nil; m, err = split.FindNextMatch(m) {
				start, length := m.ByteRange()
				want = append(want, [2]int{start, start + length})
			}
			require.NoError(t, err)
			for start, end := e.nextPiece(text, 0); start < len(text); start, end = e.nextPiece(text, end) {
				got = append(got, [2]int{start, end})
			}
			assert.Equal(t, want, got, "%s %q", model, text)
		}
	}
}

// Count gives the codec's count, cold and from the piece cache. Counting the
// codec's pieces one at a time gives the same total, which is what lets Count
// cache each piece.
func TestCountMatchesCodec(t *testing.T) {
	for model, pattern := range splitPatterns {
		e := o200kBase()
		if usesCl100k(model) {
			e = cl100kBase()
		}
		split := regexp2.MustCompile(pattern, regexp2.None)
		for _, text := range preTokenizerSamples {
			want := countChunks(text, e.codecCount)
			byPiece := countChunks(text, func(chunk string) int {
				total := 0
				m, err := split.FindStringMatch(chunk)
				for ; m != nil && err == nil; m, err = split.FindNextMatch(m) {
					total += e.codecCount(m.String())
				}
				require.NoError(t, err)
				return total
			})
			require.Equal(t, want, byPiece, "%s %q", model, text)
			assert.Equal(t, want, Count(model, text), "%s %q", model, text)
			assert.Equal(t, want, Count(model, text), "%s %q cached", model, text)
		}
	}
}

// longestInput records the longest text its codec is asked to count.
type longestInput struct {
	tokenizer.Codec
	longest int
}

func (c *longestInput) Count(text string) (int, error) {
	c.longest = max(c.longest, len(text))
	return c.Codec.Count(text)
}

// A piece that spans runs of alternating classes is never cut as one long run.
// It reaches the encoder in parts of at most maxPieceBytes, so BPE stays
// linear, and the count stays within 0.5% of encoding it whole.
func TestCountBoundsEncoderInput(t *testing.T) {
	for _, tc := range []struct {
		model string
		text  string
	}{
		{"gpt-4", strings.Repeat("\u2764\ufe0f", 2000)},
		{"gpt-4", strings.Repeat("!!\u0301", 3000)},
		{"gpt-5.6-sol", strings.Repeat("!!\u0301", 3000)},
		{"gpt-5.6-sol", "!" + strings.Repeat("\n/", 4000)},
		{"gpt-4", strings.Repeat("\u2764\ufe0f", 2000) + "\xff"},
		{"gpt-4", strings.Repeat("\xff", 5000)},
	} {
		var base tokenizer.Codec = codec.NewO200kBase()
		nextPiece := nextO200kPiece
		if usesCl100k(tc.model) {
			base, nextPiece = codec.NewCl100kBase(), nextCl100kPiece
		}
		recorder := &longestInput{Codec: base}
		got := newEncoding(recorder, nextPiece).countText(tc.text)
		whole, err := base.Count(tc.text)
		require.NoError(t, err)

		assert.LessOrEqual(t, recorder.longest, maxPieceBytes, "%s %q", tc.model, tc.text[:12])
		assert.InEpsilon(t, whole, got, 0.005, "%s %q", tc.model, tc.text[:12])
		assert.Equal(t, got, Count(tc.model, tc.text), "%s %q", tc.model, tc.text[:12])
	}
}
