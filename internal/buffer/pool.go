package buffer

import "sync"

// Pool provides a pool of byte buffers for reuse
var Pool = sync.Pool{
	New: func() interface{} {
		// Allocate 8KB buffer (common size for network I/O)
		return make([]byte, 8192)
	},
}

// Get retrieves a buffer from the pool
func Get() []byte {
	return Pool.Get().([]byte)
}

// Put returns a buffer to the pool
// The buffer should be reset before being put back
func Put(buf []byte) {
	if cap(buf) >= 8192 {
		// Only put back buffers that are at least 8KB
		Pool.Put(buf[:cap(buf)])
	}
}
