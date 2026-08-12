package archive

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type Stats struct {
	EncodedBytes   int64
	ExtractedBytes int64
	FileCount      int64
}

type countingReader struct {
	reader io.Reader
	read   int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read += int64(n)
	return n, err
}

func Extract(ctx context.Context, source io.Reader, root string) (stats Stats, err error) {
	ctx, span := otel.Tracer("published-artifact/archive").Start(ctx, "archive.extract")
	defer func() {
		span.SetAttributes(
			attribute.Int64("artifact.upload.encoded_bytes", stats.EncodedBytes),
			attribute.Int64("artifact.upload.extracted_bytes", stats.ExtractedBytes),
			attribute.Int64("artifact.upload.file_count", stats.FileCount),
		)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()
	counted := &countingReader{reader: source}
	defer func() { stats.EncodedBytes = counted.read }()
	reader := tar.NewReader(counted)
	for {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return stats, nil
		}
		if err != nil {
			return stats, fmt.Errorf("read tar archive: %w", err)
		}
		if err := validatePath(header.Name); err != nil {
			return stats, err
		}
		target := filepath.Join(root, filepath.FromSlash(header.Name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return stats, fmt.Errorf("create artifact directory: %w", err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return stats, fmt.Errorf("create artifact parent: %w", err)
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
			if err != nil {
				return stats, fmt.Errorf("create artifact file: %w", err)
			}
			written, copyErr := copyContext(ctx, file, reader)
			closeErr := file.Close()
			stats.ExtractedBytes += written
			stats.FileCount++
			if copyErr != nil {
				return stats, fmt.Errorf("extract artifact file: %w", copyErr)
			}
			if closeErr != nil {
				return stats, fmt.Errorf("close artifact file: %w", closeErr)
			}
		default:
			return stats, fmt.Errorf("unsupported tar entry type %d", header.Typeflag)
		}
	}
}

func validatePath(name string) error {
	if name == "" || !utf8.ValidString(name) || path.IsAbs(name) || strings.Contains(name, "\\") {
		return fmt.Errorf("invalid artifact path %q", name)
	}
	trimmed := strings.TrimSuffix(name, "/")
	if trimmed == "" {
		return fmt.Errorf("invalid artifact path %q", name)
	}
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("invalid artifact path %q", name)
		}
		for _, character := range segment {
			if unicode.IsControl(character) {
				return fmt.Errorf("invalid artifact path %q", name)
			}
		}
	}
	return nil
}

func copyContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	buffer := make([]byte, 32*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			written, writeErr := destination.Write(buffer[:count])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != count {
				return total, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return total, nil
			}
			return total, readErr
		}
	}
}
