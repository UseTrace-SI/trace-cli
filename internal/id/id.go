package id

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sync"
	"time"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var entropyMu sync.Mutex
var lastMS uint64
var lastEntropy [10]byte

func New() (string, error) {
	var b [16]byte
	entropyMu.Lock()
	defer entropyMu.Unlock()
	ms := uint64(time.Now().UnixMilli())
	if ms < lastMS {
		ms = lastMS
	}
	if ms == lastMS {
		for i := len(lastEntropy) - 1; i >= 0; i-- {
			lastEntropy[i]++
			if lastEntropy[i] != 0 {
				break
			}
			if i == 0 {
				ms++
				if _, err := rand.Read(lastEntropy[:]); err != nil {
					return "", fmt.Errorf("creating ULID: %w", err)
				}
			}
		}
	} else if _, err := rand.Read(lastEntropy[:]); err != nil {
		return "", fmt.Errorf("creating ULID: %w", err)
	}
	lastMS = ms
	binary.BigEndian.PutUint64(b[:8], ms<<16)
	copy(b[6:], lastEntropy[:])
	var encoded [26]byte
	for i := range encoded {
		var value byte
		for bit := 0; bit < 5; bit++ {
			index := i*5 + bit - 2
			value <<= 1
			if index >= 0 {
				value |= (b[index/8] >> (7 - index%8)) & 1
			}
		}
		encoded[i] = crockford[value]
	}
	return string(encoded[:]), nil
}
