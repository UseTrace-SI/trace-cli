package syncclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
)

type networkManifest struct {
	Chunks []blob.Hash `json:"chunks"`
}

func (c *Client) uploadChunked(ctx context.Context, hash string, chunks []blob.Hash) error {
	if len(chunks) > 100000 {
		return errors.New("blob exceeds the protocol's 100000-chunk limit")
	}
	unique := make([]string, 0, len(chunks))
	seen := map[string]bool{}
	for _, h := range chunks {
		if !seen[string(h)] {
			unique = append(unique, string(h))
			seen[string(h)] = true
		}
	}
	for start := 0; start < len(unique); start += blobHaveBatchSize {
		batch := unique[start:min(start+blobHaveBatchSize, len(unique))]
		var reply struct {
			Need []string `json:"need"`
		}
		if err := c.post(ctx, "/v1/sync/chunks/have", map[string]any{"repo": c.Config.Remote, "mode": c.Config.Mode, "hashes": batch}, &reply); err != nil {
			return fmt.Errorf("checking chunks (requires sync protocol 2): %w", err)
		}
		allowed := map[string]bool{}
		for _, h := range batch {
			allowed[h] = true
		}
		for _, h := range reply.Need {
			if !allowed[h] {
				return errors.New("sync server requested an unknown chunk")
			}
			delete(allowed, h)
			r, err := c.Blobs.GetChunk(blob.Hash(h))
			if err != nil {
				return err
			}
			data, err := io.ReadAll(io.LimitReader(r, 64*1024+1))
			closeErr := r.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			if len(data) > 64*1024 {
				return errors.New("local chunk exceeds 64 KiB")
			}
			payload := base64.StdEncoding.EncodeToString(data)
			request := map[string]any{"repo": c.Config.Remote, "mode": c.Config.Mode, "hash": h}
			if c.Cipher != nil {
				payload, err = c.Cipher.Seal(data, "chunk:"+h)
				if err != nil {
					return err
				}
				request["salt"] = base64.StdEncoding.EncodeToString(c.Cipher.Salt)
			}
			request["payload"] = payload
			if err := c.post(ctx, "/v1/sync/chunks", request, nil); err != nil {
				return err
			}
		}
	}
	data, err := json.Marshal(networkManifest{Chunks: chunks})
	if err != nil {
		return err
	}
	payload := base64.StdEncoding.EncodeToString(data)
	request := map[string]any{"repo": c.Config.Remote, "mode": c.Config.Mode, "hash": hash, "encoding": "chunked-v1"}
	if c.Cipher != nil {
		payload, err = c.Cipher.Seal(data, "manifest:"+hash)
		if err != nil {
			return err
		}
		request["salt"] = base64.StdEncoding.EncodeToString(c.Cipher.Salt)
	}
	request["payload"] = payload
	return c.post(ctx, "/v1/sync/blobs", request, nil)
}

func (c *Client) pullChunked(ctx context.Context, bs *blob.Store, hash string, data []byte, passphrase string, ciphers map[string]*Cipher) error {
	var manifest networkManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("decoding chunk manifest: %w", err)
	}
	if len(manifest.Chunks) == 0 || len(manifest.Chunks) > 100000 {
		return errors.New("invalid chunk manifest size")
	}
	seen := map[blob.Hash]bool{}
	for _, h := range manifest.Chunks {
		if !downloadHash.MatchString(string(h)) {
			return errors.New("invalid manifest chunk hash")
		}
		if seen[h] {
			continue
		}
		seen[h] = true
		if local, err := bs.GetChunk(h); err == nil {
			_, err := io.Copy(io.Discard, local)
			closeErr := local.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		query := c.downloadValues()
		query.Set("hash", string(h))
		var item struct {
			Hash    string `json:"hash"`
			Payload string `json:"payload"`
			Salt    string `json:"salt"`
		}
		if err := c.get(ctx, "/v1/sync/chunks", query, &item); err != nil {
			return err
		}
		if item.Hash != string(h) {
			return errors.New("sync server returned the wrong chunk")
		}
		plain, err := c.openDownloaded(item.Payload, item.Salt, "chunk:"+string(h), passphrase, ciphers)
		if err != nil {
			return err
		}
		if err := bs.PutChunk(h, plain); err != nil {
			return err
		}
	}
	return bs.PutChunked(blob.Hash(hash), manifest.Chunks)
}
