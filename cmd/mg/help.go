package main

// helpRequested reports whether a raw argument slice contains a help flag
// (--help or -h).
//
// Commands that set DisableFlagParsing:true bypass cobra's built-in --help/-h
// interception — cobra hands every token, help flags included, straight to
// RunE. Such commands MUST call this at the very top of RunE, before any side
// effect, and return cmd.Help() when it is true. Without the guard a
// passthrough command treats "--help" as ordinary input (e.g. `mg event append
// --help` would persist a junk {"type":"--help"} event, and `mg log --help`
// would exec git log instead of showing usage). See drellem2/pogo#54.
//
// Factoring the check here means any future DisableFlagParsing command inherits
// the same safety by calling helpRequested(args) up front.
func helpRequested(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return true
		}
	}
	return false
}
