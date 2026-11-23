package constant

var StreamingTimeout int
var DifyDebug bool
var MaxFileDownloadMB int
var StreamScannerMaxBufferMB int
var ForceStreamOption bool
var CountToken bool
var GetMediaToken bool
var GetMediaTokenNotStream bool
var UpdateTask bool
var MaxRequestBodyMB int

// MaxResponseBodyMB bounds an upstream response body the relay reads whole, in
// MiB; a non-positive value means the default.
var MaxResponseBodyMB = DefaultMaxResponseBodyMB

// DefaultMaxResponseBodyMB matches the default limits of a request body and of
// one stream event, and leaves room for a non-stream answer that carries
// several base64 images.
const DefaultMaxResponseBodyMB = 128

// PluginMemoryLimitMB is the memory the JavaScript of one plugin hook call may
// keep, in MiB; 0 means no limit.
var PluginMemoryLimitMB = 512

var AnonymousRequestBodyLimitKB int
var AzureDefaultAPIVersion string
var NotifyLimitCount int
var NotificationLimitDurationMinute int
var GenerateDefaultToken bool
var ErrorLogEnabled bool
var TaskQueryLimit int
var TaskTimeoutMinutes int
var TaskPollMaxFailures = 20
var TaskPluginProtocolTimeoutSeconds int
var TaskPluginProtocolTickMilliseconds int
var TaskPluginProtocolTickJitterMilliseconds int
var TaskPluginProtocolHeartbeatSeconds int

// temporary variable for sora patch, will be removed in future
var TaskPricePatches []string

// TrustedRedirectDomains is a list of trusted domains for redirect URL validation.
// Domains support subdomain matching (e.g., "example.com" matches "sub.example.com").
var TrustedRedirectDomains []string
