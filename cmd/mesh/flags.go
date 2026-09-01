package main

import (
	"flag"
	"strings"
)

// hoistFlags moves flag arguments ahead of positional ones.
//
// Go's flag package stops parsing at the first non-flag token, so
// `mesh ask opencode --timeout 3m "how are you"` would otherwise fold
// "--timeout 3m" into the question and silently use the default timeout.
// Agents and humans both write flags after the peer name, so accept it.
//
// A dash-prefixed token that is not a flag of this command stays positional,
// which keeps a question like "-v is failing" intact. Everything after a bare
// "--" is positional.
func hoistFlags(fs *flag.FlagSet, args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			if fs.Lookup(name[:eq]) != nil {
				flags = append(flags, a)
			} else {
				pos = append(pos, a)
			}
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		if !isBoolFlag(f) && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, pos...)
}

func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}
