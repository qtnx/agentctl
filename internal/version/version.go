package version

import "fmt"

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func String() string {
	return fmt.Sprintf("agentctl %s (%s, %s)", valueOrDefault(Version, "dev"), valueOrDefault(Commit, "none"), valueOrDefault(Date, "unknown"))
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
