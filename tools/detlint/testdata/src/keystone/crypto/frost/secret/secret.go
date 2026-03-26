package secret

import "crypto/rand"

func nonce() []byte {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return b
}
