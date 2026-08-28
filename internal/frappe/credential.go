package frappe

// The bridged credential's env var names. The password var is exported so the
// engine fake can gate its private repositories on the same contract.
const (
	credUsernameVar       = "TAMP_GIT_USERNAME"
	CredentialPasswordVar = "TAMP_GIT_PASSWORD"
)

// CredentialEnv is the bridge's in-container half (ADR 0002): env-based git
// config names an inline helper that reads the secret back out of the same
// environment — no file, no command line — scoped to one host.
func CredentialEnv(protocol, host, username, password string) []string {
	scope := "credential." + protocol + "://" + host + ".helper"
	return []string{
		"GIT_TERMINAL_PROMPT=0",
		credUsernameVar + "=" + username,
		CredentialPasswordVar + "=" + password,
		"GIT_CONFIG_COUNT=2",
		// The empty value first resets any configured helper list, so none
		// can store the secret at rest.
		"GIT_CONFIG_KEY_0=" + scope,
		"GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=" + scope,
		"GIT_CONFIG_VALUE_1=" + envHelper,
	}
}

// envHelper answers only "get" — the host completes store and erase.
const envHelper = `!f() { if [ "$1" = get ]; then printf 'username=%s\npassword=%s\n' "$TAMP_GIT_USERNAME" "$TAMP_GIT_PASSWORD"; fi; }; f`
