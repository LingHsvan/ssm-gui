// Copyright (C) 2024, 2025 kvarenzn
// SPDX-License-Identifier: GPL-3.0-or-later

package k

import (
	"bytes"
	"crypto/aes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"regexp"
)

// BanG Dream! Our Notes (sirius) bundles are encrypted only in their first
// 16 KiB with an AES-128-CTR-like keystream. The nonce is derived from the
// original CDN bundle filename (without any path).
var (
	siriusUnitySignature = []byte("UnityFS\x00")
	siriusCipherKey      = []byte{
		0x73, 0x72, 0xa4, 0xee, 0x77, 0x7d, 0xb3, 0x61,
		0xad, 0x89, 0x6c, 0x99, 0xe4, 0x08, 0xa1, 0x82,
	}
	siriusNonceSeed = []byte{0xee, 0x24, 0xa7, 0x02, 0x38, 0xe2, 0xa0, 0xe5}
	siriusHeaderLen = 16 * 1024

	// The game caches downloaded bundles under a decorated name
	// "<32-hex>_<original name without .bundle>_<16-hex>.bundle", while the
	// encryption nonce is seeded with the original CDN filename.
	siriusDecoratedName = regexp.MustCompile(`^[0-9a-f]{32}_(.+)_([0-9a-f]{16})\.bundle$`)
)

// SiriusFilename maps an on-disk bundle filename to the plain CDN filename
// used for nonce derivation. Names that are not decorated are returned as-is.
func SiriusFilename(name string) string {
	if m := siriusDecoratedName.FindStringSubmatch(name); m != nil {
		return m[1] + ".bundle"
	}

	return name
}

// NewSiriusAssetFile wraps a sirius bundle stream and decrypts its protected
// header in memory. filename must be the plain CDN bundle filename (see
// SiriusFilename).
func NewSiriusAssetFile(reader io.Reader, filename string) (io.Reader, error) {
	header := make([]byte, siriusHeaderLen)
	n, err := io.ReadFull(reader, header)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	header = header[:n]

	if bytes.HasPrefix(header, siriusUnitySignature) {
		return io.MultiReader(bytes.NewReader(header), reader), nil
	}

	seed := append([]byte{}, siriusNonceSeed...)
	seed = append(seed, filename...)
	sum := sha256.Sum256(seed)
	nonce := sum[:8]

	block, err := aes.NewCipher(siriusCipherKey)
	if err != nil {
		return nil, err
	}

	var counter, mask [16]byte
	copy(counter[:], nonce)
	for offset := 0; offset < n; offset += 16 {
		binary.BigEndian.PutUint64(counter[8:], uint64(offset/16))
		block.Encrypt(mask[:], counter[:])
		end := min(offset+16, n)
		for i := offset; i < end; i++ {
			header[i] ^= mask[i-offset]
		}
	}

	if !bytes.HasPrefix(header, siriusUnitySignature) {
		return nil, fmt.Errorf("sirius bundle header did not decrypt to UnityFS: %s", filename)
	}

	return io.MultiReader(bytes.NewReader(header), reader), nil
}
