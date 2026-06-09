package main

// ANSI colors — kept identical to the original Python statusline so the visual
// output is byte-for-byte compatible.
const (
	dim = "\033[2m"
	rst = "\033[0m"
	grn = "\033[32m"
	ylw = "\033[33m"
	red = "\033[31m"
	cyn = "\033[36m"
	bld = "\033[1m"
	mag = "\033[35m"
)

// Segment separator ( | ) and inline dot ( · ), both dimmed.
var (
	sep = " " + dim + "|" + rst + " "
	dot = " " + dim + "·" + rst + " "
)
