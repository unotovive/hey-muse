package voice

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// sentenceCursor hands out the sentences of an answer that is still being written, each once, so
// each can be spoken as soon as it is complete. full is the whole answer so far, already through
// speech.Spoken.
type sentenceCursor struct {
	spoken int    // bytes of last already handed out
	last   string // the text those bytes were when they were handed out
	count  int    // sentences handed out, for finding the place again in a rewritten final text
}

// Next returns the complete sentences of full not returned before; with final, the unfinished tail
// too. When the server has rewritten what was already handed out (full no longer starts with it),
// nothing is returned until the final text, which resumes after as many sentences as were said:
// what was said is never said again, and the end of the answer is never left out.
func (c *sentenceCursor) Next(full string, final bool) []string {
	if !strings.HasPrefix(full, c.last) {
		if !final {
			return nil
		}
		all := speakable(sentencesOf(full))
		if len(all) <= c.count {
			return nil // an earlier, shorter text: everything in it has been said
		}
		out := all[c.count:]
		c.spoken, c.last, c.count = len(full), full, len(all)
		return out
	}
	complete, rest := splitSentences(full[c.spoken:])
	c.spoken = len(full) - len(rest)
	if final && strings.TrimSpace(rest) != "" {
		complete = append(complete, strings.TrimSpace(rest))
		c.spoken = len(full)
	}
	c.last = full[:c.spoken]
	out := speakable(complete)
	c.count += len(out)
	return out
}

// sentencesOf is every sentence of s, its unfinished tail included.
func sentencesOf(s string) []string {
	complete, rest := splitSentences(s)
	if rest = strings.TrimSpace(rest); rest != "" {
		complete = append(complete, rest)
	}
	return complete
}

// speakable leaves out pieces with nothing to say: a mark alone, left over when a snapshot ended
// between "！" and "？".
func speakable(pieces []string) []string {
	var out []string
	for _, p := range pieces {
		if strings.IndexFunc(p, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) >= 0 {
			out = append(out, p)
		}
	}
	return out
}

const (
	sentenceEnds = "。！？!?"
	closers      = "」』）)】〉》”’\"'"
	openers      = "「『（(【〈《“‘"
)

// splitSentences cuts s after each run of 。！？!? (with the closing brackets and quotes right after
// it) and at each run of line breaks, and returns the sentences, trimmed, and what follows the last
// cut. A mark inside brackets ends nothing: 「本当？」と言った。 is one sentence. A period is not
// a sentence end, so 3.5 stays whole.
func splitSentences(s string) (complete []string, rest string) {
	start, depth := 0, 0
	cut := func(end, next int) {
		if piece := strings.TrimSpace(s[start:end]); piece != "" {
			complete = append(complete, piece)
		}
		start = next
	}
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '\n' || r == '\r':
			end := i
			for i < len(s) && (s[i] == '\n' || s[i] == '\r') {
				i++
			}
			cut(end, i)
			depth = 0
			continue
		case strings.ContainsRune(openers, r):
			depth++
		case strings.ContainsRune(closers, r) && depth > 0:
			depth--
		case strings.ContainsRune(sentenceEnds, r) && depth == 0:
			i += n
			for i < len(s) {
				r, n := utf8.DecodeRuneInString(s[i:])
				if !strings.ContainsRune(sentenceEnds, r) && !strings.ContainsRune(closers, r) {
					break
				}
				i += n
			}
			cut(i, i)
			continue
		}
		i += n
	}
	return complete, strings.TrimLeftFunc(s[start:], unicode.IsSpace)
}
