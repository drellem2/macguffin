package mgerr

import "strings"

// cobraUsagePrefixes are the leading substrings of the plain errors cobra
// generates for caller misuse. Because rootCmd sets SilenceErrors+SilenceUsage,
// these arrive at the exit seam as ordinary errors with no type to match on, so
// we classify them by their (stable, cobra-owned) message prefixes. The primary
// path for flag/arg errors is the FlagErrorFunc + UsageArgs wrappers in cmd/mg,
// which produce a typed *Error directly; this matcher is the safety net for the
// unknown-subcommand case (§6.3) and any usage error those wrappers don't cover.
var cobraUsagePrefixes = []string{
	"unknown command",
	"unknown flag",
	"unknown shorthand flag",
	"flag needs an argument",
	"invalid argument",
	"accepts ",  // "accepts 1 arg(s), received 0"
	"requires ", // "requires at least 1 arg(s)"
}

// isCobraUsageError reports whether err looks like a cobra-generated
// caller-misuse error that should map to the Usage category (exit 2).
func isCobraUsageError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, p := range cobraUsagePrefixes {
		if strings.HasPrefix(msg, p) {
			return true
		}
	}
	return false
}
