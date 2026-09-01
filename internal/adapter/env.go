package adapter

import "os"

func envOf() []string { return os.Environ() }
