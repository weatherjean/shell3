package luacfg

// lockVM takes the VM mutex, returning an unlock func. Callers use it as
// `defer c.lockVM()()`. It guards the single Lua VM while WrapBash drives it.
func (c *LoadedConfig) lockVM() func() {
	c.mu.Lock()
	return func() { c.mu.Unlock() }
}
