package main

import "regexp"

// ansiSeq matches the SGR color escapes this status line emits, so they can be
// stripped before measuring how many columns a string actually occupies.
var ansiSeq = regexp.MustCompile("\x1b\\[[0-9;]*m")

// visibleWidth returns the rendered column width of s: ANSI color escapes are
// ignored and wide glyphs (emoji, CJK) count as two columns. It is a heuristic
// — exactness isn't possible without the terminal's font — but it errs toward
// over-counting ambiguous symbols so the line wraps a hair early rather than
// being truncated.
func visibleWidth(s string) int {
	s = ansiSeq.ReplaceAllString(s, "")
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

// runeWidth approximates the terminal column width of a single rune: 0 for
// zero-width marks, 2 for wide/emoji glyphs, 1 otherwise. Cyrillic and Latin
// (the bulk of branch and task names) fall through to 1.
func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r >= 0x0300 && r <= 0x036F, // combining diacritical marks
		r >= 0x200B && r <= 0x200F, // zero-width spaces / directional marks
		r >= 0xFE00 && r <= 0xFE0F, // variation selectors
		r == 0xFEFF:                // zero-width no-break space
		return 0
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2600 && r <= 0x27BF, // misc symbols + dingbats (⚡⚠✓✗✅❌📝)
		r >= 0x2E80 && r <= 0xA4CF, // CJK, Kangxi, Hiragana/Katakana, …
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK compatibility ideographs
		r >= 0xFE30 && r <= 0xFE4F, // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFF60, // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6, // fullwidth signs
		r >= 0x1F000:               // emoji, pictographs, supplemental symbols
		return 2
	}
	return 1
}
