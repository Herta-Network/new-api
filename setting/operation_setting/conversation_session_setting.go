package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// ConversationSessionSetting controls conversation session recognition for
// chat, responses, messages and Gemini requests. Recognition only runs for a
// request whose session someone asks for: a channel affinity rule with the
// conversation_session key source, or a channel that needs a session, such as
// OpenCode Go. Enabled turns it off entirely.
type ConversationSessionSetting struct {
	Enabled bool `json:"enabled"`
	// WindowSeconds is how long a stored session clue can link a later request,
	// counted from the call that produced it.
	WindowSeconds int `json:"window_seconds"`
	// MaxEntries bounds the in-memory clue cache. It is read when the cache is
	// first built, so a change takes effect after a restart.
	MaxEntries int `json:"max_entries"`
}

var conversationSessionSetting = ConversationSessionSetting{
	Enabled:       true,
	WindowSeconds: 24 * 60 * 60,
	MaxEntries:    100_000,
}

func init() {
	config.GlobalConfig.Register("conversation_session_setting", &conversationSessionSetting)
}

func GetConversationSessionSetting() *ConversationSessionSetting {
	return &conversationSessionSetting
}
