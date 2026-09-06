package commitartifact

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// batchRequests avoids constructing another object-ID list in memory. exec feeds
// stdin while the caller drains stdout, so a full pipe cannot deadlock the batch.
type batchRequests struct {
	files   []*publicationFile
	index   int
	pending string
}

func (r *batchRequests) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.pending == "" {
		if r.index == len(r.files) {
			return 0, io.EOF
		}
		r.pending = r.files[r.index].oid + "\n"
		r.index++
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func (r gitRepo) materializeBlobs(ctx context.Context, root string, files []*publicationFile) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	return r.stream(ctx, &batchRequests{files: files}, func(input io.Reader) error {
		return materializeBatch(ctx, input, root, files)
	}, "cat-file", "--batch")
}

// stream drains bounded protocol records and reaps the child before returning.
func (r gitRepo) stream(ctx context.Context, input io.Reader, consume func(io.Reader) error, args ...string) (err error) {
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := r.command(childCtx, args...)
	cmd.Stdin = input
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		stdout.Close()
		return fmt.Errorf("retained commit git stream: %w", err)
	}
	defer func() {
		if err != nil {
			cancel()
		}
		waitErr := cmd.Wait()
		if ctx.Err() != nil {
			err = ctx.Err()
		} else if err == nil && waitErr != nil {
			err = fmt.Errorf("retained commit git stream: %w", waitErr)
		}
	}()
	return consume(stdout)
}

func materializeBatch(ctx context.Context, input io.Reader, root string, files []*publicationFile) error {
	reader := bufio.NewReaderSize(publicationReader{ctx, input}, 4096)
	for _, file := range files {
		header, err := reader.ReadSlice('\n')
		if err != nil {
			return err
		}
		fields := strings.Fields(string(header))
		if len(fields) != 3 || fields[0] != file.oid || fields[1] != "blob" {
			return ErrInvalid
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size != file.size {
			return ErrInvalid
		}
		dest := filepath.Join(root, filepath.FromSlash(file.path))
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return err
		}
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, file.mode)
		if err != nil {
			return err
		}
		_, err = io.CopyN(f, publicationReader{ctx, reader}, size)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		delimiter, err := reader.ReadByte()
		if err != nil {
			return err
		}
		if delimiter != '\n' {
			return ErrInvalid
		}
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		if err != nil {
			return err
		}
		return ErrInvalid
	}
	return ctx.Err()
}
