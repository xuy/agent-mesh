package main

import "strings"

// fileList collects a repeatable --file flag.
type fileList []string

func (f *fileList) String() string { return strings.Join(*f, ", ") }

func (f *fileList) Set(v string) error {
	*f = append(*f, v)
	return nil
}
