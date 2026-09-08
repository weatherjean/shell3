package strutil

import "fmt"

// Capture keeps a bounded head and tail while counting discarded bytes.
// One writer owns it; callers serialize concurrent writes if necessary.
type Capture struct {
	Limit      int
	head, tail []byte
	total      int
}

func (c *Capture) Write(p []byte) (int, error) {
	n := len(p)
	c.total += n
	half := max(0, c.Limit/2)
	keep := min(len(p), max(0, half-len(c.head)))
	c.head = append(c.head, p[:keep]...)
	p = p[keep:]
	if len(p) >= half {
		c.tail = append(c.tail[:0], p[len(p)-half:]...)
	} else {
		if excess := len(c.tail) + len(p) - half; excess > 0 {
			copy(c.tail, c.tail[excess:])
			c.tail = c.tail[:len(c.tail)-excess]
		}
		c.tail = append(c.tail, p...)
	}
	return n, nil
}

func (c *Capture) Len() int { return c.total }

func (c *Capture) String() string {
	if discarded := c.total - len(c.head) - len(c.tail); discarded > 0 {
		return fmt.Sprintf("%s\n... [%d bytes elided] ...\n%s", c.head, discarded, c.tail)
	}
	return string(c.head) + string(c.tail)
}
