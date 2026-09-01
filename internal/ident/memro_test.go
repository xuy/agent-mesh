package ident

import "go4.org/mem"

func memRO(b [32]byte) mem.RO { return mem.B(b[:]) }
