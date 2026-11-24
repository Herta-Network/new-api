package mistral

// ModelList holds the models Mistral's documentation listed as current on
// 2026-11-24, with mistral-vibe-cli-latest, the model Mistral Vibe runs by
// default besides Mistral Large 4.
var ModelList = []string{
	"mistral-large-4-0",
	"mistral-large-4",
	"mistral-vibe-cli-latest",
	"mistral-medium-3-5",
	"mistral-small-latest",
	"mistral-small-2603",
	"codestral-latest",
	"codestral-2508",
	"mistral-embed",
	"codestral-embed",
}

var ChannelName = "mistral"

// The channel sends its requests the way Mistral Vibe does. The values below
// are read from github.com/mistralai/mistral-vibe at commit
// e76235b203b8965af584e88e69284bc704c31a1f (release 3.1.4); compare a newer
// release against them when following Vibe.
const (
	// VibeVersion is the Vibe release this channel presents itself as.
	VibeVersion = "3.1.4"
	// UserAgent is the User-Agent of Vibe's Mistral requests:
	// get_user_agent("mistral") in vibe/utils/http.py, which replaces the
	// Mistral SDK's own.
	UserAgent = "mistral-client-python/Mistral-Vibe/" + VibeVersion
	// billingUserAgent is the User-Agent of Vibe's /v1/billing reads
	// (vibe/app_server/_billing.py), which set none of their own and send
	// httpx2's default; uv.lock pins httpx2 2.16.0.
	billingUserAgent = "python-httpx2/2.16.0"
	// affinityHeader carries the conversation a request belongs to, which
	// Mistral routes on to reuse its prompt cache: request_headers in
	// harness/.../vibe/_runtime_config.py.
	affinityHeader = "x-affinity"
	// defaultTemperature is what Vibe sends when the model's settings name no
	// temperature: ModelConfig.temperature in vibe/core/config/models.py.
	defaultTemperature = 1.0
	// maxRequestImages is how many images Vibe keeps in one request, the most
	// recent ones; older images become droppedImageText (adapters/mistral.py).
	maxRequestImages = 8
	droppedImageText = "[Image removed: only the 8 most recent images are sent]"
)

// defaultReasoningEffort is the reasoning effort Vibe sends to its own
// default models, whose settings think at high (DEFAULT_MODELS in
// vibe/core/config/vibe_schema.py).
var defaultReasoningEffort = map[string]string{
	"mistral-large-4-0":       "high",
	"mistral-vibe-cli-latest": "high",
}

// reasoningSupport is how a model takes reasoning_effort: the levels it
// accepts, lowest first, and whether it accepts none. A model with no levels
// refuses the member altogether.
type reasoningSupport struct {
	levels     []string
	canTurnOff bool
}

// modelReasoning holds the models whose reasoning Mistral's API was seen to
// accept on 2026-11-24, by every name /v1/models lists for them; it agrees
// with Vibe's table (harness/.../vibe/completion_profile/known_models.py).
// Other models are unknown and get the client's effort as written.
var modelReasoning = func() map[string]reasoningSupport {
	high := reasoningSupport{levels: []string{"high"}, canTurnOff: true}
	large4 := reasoningSupport{levels: []string{"low", "medium", "high"}}
	glm := reasoningSupport{levels: []string{"low", "high", "max"}}
	none := reasoningSupport{}
	families := []struct {
		support reasoningSupport
		names   []string
	}{
		// Mistral Medium 3.5, which Vibe runs as mistral-vibe-cli-latest.
		{high, []string{"mistral-medium-latest", "mistral-medium", "mistral-medium-3-5", "mistral-medium-3.5", "mistral-medium-3",
			"mistral-medium-2604", "mistral-vibe-cli-latest", "mistral-vibe-cli-with-tools", "magistral-medium-latest"}},
		// Mistral Small 4, Vibe's mistral-vibe-cli-fast.
		{high, []string{"mistral-small-latest", "mistral-small-2603", "mistral-vibe-cli-fast", "magistral-small-latest"}},
		{large4, []string{"mistral-large-latest", "mistral-large-4", "mistral-large-4-0"}},
		{glm, []string{"zai-glm-5-3", "zai-glm-5", "zai-glm-latest", "glm-5-latest"}},
		{none, []string{"mistral-large-2512", "codestral-latest", "codestral-2508", "ministral-14b-latest", "ministral-14b-2512",
			"ministral-8b-latest", "ministral-8b-2512", "ministral-3b-latest", "ministral-3b-2512"}},
	}
	table := make(map[string]reasoningSupport)
	for _, family := range families {
		for _, name := range family.names {
			table[name] = family.support
		}
	}
	return table
}()

// standardEfforts ranks the reasoning efforts clients send, lowest first.
var standardEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// maxChoices is the most choices Mistral returns for one request; it refuses
// a larger n ("Max allowed: 10").
const maxChoices = 10
