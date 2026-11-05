package history

import "strings"

// StreamRedactor withholds possible secret prefixes at chunk boundaries, then
// redacts them when the next chunk arrives. Callers serialize Feed calls.
type StreamRedactor struct {
	pending map[string]string
}

func (r *StreamRedactor) Feed(stream, value string, secrets []string) string {
	if r.pending == nil {
		r.pending = make(map[string]string)
	}
	value = r.pending[stream] + value
	keep := 0
	for _, secret := range secrets {
		for size := min(len(secret)-1, len(value)); size > keep; size-- {
			if strings.HasSuffix(value, secret[:size]) {
				keep = size
				break
			}
		}
	}
	r.pending[stream] = value[len(value)-keep:]
	return Sanitize(value[:len(value)-keep], secrets)
}
