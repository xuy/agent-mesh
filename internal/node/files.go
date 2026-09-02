package node

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/xuy/agent-mesh/internal/config"
	"github.com/xuy/agent-mesh/internal/wire"
)

// chunkSize is how much of a file travels in one frame. Large enough that a
// big file is not thousands of frames, small enough that a slow reader is not
// waiting on megabytes.
const chunkSize = 256 * 1024

// MaxFileSize caps an attachment. A mesh peer can make this node write to disk,
// so the limit exists whether or not anyone would ever hit it.
const MaxFileSize = 100 << 20

// describeFiles reads the attachments a caller wants to send, without loading
// them, and returns what to announce.
func describeFiles(paths []string) ([]wire.File, error) {
	var out []wire.File
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("cannot attach %s: %w", p, err)
		}
		if st.IsDir() {
			return nil, fmt.Errorf("cannot attach %s: it is a directory", p)
		}
		if st.Size() > MaxFileSize {
			return nil, fmt.Errorf("cannot attach %s: %d bytes is over the %d byte limit", p, st.Size(), MaxFileSize)
		}
		sum, err := sumFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, wire.File{Name: filepath.Base(p), Size: st.Size(), Sum: sum, Path: p})
	}
	return out, nil
}

func sumFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// sendFiles streams announced attachments after the message that announced
// them. Path is local-only, so it is cleared before anything goes on the wire.
func sendFiles(wc *wire.Conn, from, to, corr string, files []wire.File) error {
	for i, f := range files {
		src, err := os.Open(f.Path)
		if err != nil {
			return err
		}
		buf := make([]byte, chunkSize)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				frame := wire.Envelope{
					Corr: corr, From: from, To: to, Kind: wire.KindFile,
					Index: i, Chunk: append([]byte(nil), buf[:n]...),
				}
				if serr := wc.Send(frame); serr != nil {
					src.Close()
					return serr
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				src.Close()
				return err
			}
		}
		src.Close()
		if err := wc.Send(wire.Envelope{
			Corr: corr, From: from, To: to, Kind: wire.KindFile, Index: i, Last: true,
		}); err != nil {
			return err
		}
	}
	return nil
}

// receiveFiles reads the announced attachments into the node's file store and
// fills in where each landed.
//
// A message and its attachments are one unit: if a transfer is cut short the
// message is refused rather than delivered with a half file, because an agent
// handed a truncated log will reason about it as though it were whole.
func (n *Node) receiveFiles(wc *wire.Conn, msgID string, files []wire.File) ([]wire.File, error) {
	dir := filepath.Join(config.FilesDir(n.cfg.Name), msgID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	out := make([]wire.File, len(files))
	copy(out, files)

	open := map[int]*os.File{}
	sums := map[int]interface{ Write([]byte) (int, error) }{}
	hashers := map[int]interface {
		Sum([]byte) []byte
		Write([]byte) (int, error)
	}{}
	defer func() {
		for _, f := range open {
			f.Close()
		}
	}()

	for i, f := range files {
		path := filepath.Join(dir, safeName(f.Name))
		dst, err := os.Create(path)
		if err != nil {
			return nil, err
		}
		open[i] = dst
		h := sha256.New()
		hashers[i] = h
		sums[i] = h
		out[i].Path = path
	}

	remaining := len(files)
	for remaining > 0 {
		e, err := wc.Recv()
		if err != nil {
			return nil, fmt.Errorf("attachment transfer stopped early: %w", err)
		}
		if e.Kind != wire.KindFile {
			return nil, fmt.Errorf("expected an attachment, got a %s frame", e.Kind)
		}
		if e.Index < 0 || e.Index >= len(files) {
			return nil, fmt.Errorf("attachment frame refers to file %d, which was not announced", e.Index)
		}
		if len(e.Chunk) > 0 {
			if _, err := open[e.Index].Write(e.Chunk); err != nil {
				return nil, err
			}
			hashers[e.Index].Write(e.Chunk)
		}
		if e.Last {
			remaining--
			got := hex.EncodeToString(hashers[e.Index].Sum(nil))
			if files[e.Index].Sum != "" && got != files[e.Index].Sum {
				return nil, fmt.Errorf("%s arrived corrupted or truncated", files[e.Index].Name)
			}
		}
	}
	return out, nil
}

// safeName strips anything that would let an attachment name escape the
// directory it is being written into. A peer chooses this string.
func safeName(name string) string {
	name = filepath.Base(filepath.FromSlash(name))
	name = strings.TrimLeft(name, ".")
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case strings.ContainsRune("._- ()[]+,", r):
			return r
		default:
			return '_'
		}
	}, name)
	if name == "" {
		return "attachment"
	}
	if len(name) > 120 {
		name = name[:120]
	}
	return name
}

// stripPaths removes the local path before an announcement goes on the wire.
// Where a file happens to live on this machine is nobody else's business, and
// leaking it would hand a peer a map of the sender's filesystem.
func stripPaths(files []wire.File) []wire.File {
	out := make([]wire.File, len(files))
	for i, f := range files {
		f.Path = ""
		out[i] = f
	}
	return out
}
