package sourcearchive

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func Create(ctx context.Context, destination io.Writer, source string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	writer := tar.NewWriter(destination)
	if info.IsDir() {
		err = filepath.Walk(source, func(current string, currentInfo os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if current == source {
				return nil
			}
			relative, err := filepath.Rel(source, current)
			if err != nil {
				return err
			}
			return writeTarEntry(ctx, writer, current, filepath.ToSlash(relative), currentInfo)
		})
	} else {
		err = writeTarEntry(ctx, writer, source, filepath.Base(source), info)
	}
	if err != nil {
		_ = writer.Close()
		return fmt.Errorf("package source: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish source archive: %w", err)
	}
	return nil
}

func writeTarEntry(ctx context.Context, writer *tar.Writer, source, name string, info os.FileInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	linkTarget := ""
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(source)
		if err != nil {
			return err
		}
		linkTarget = target
	}
	header, err := tar.FileInfoHeader(info, linkTarget)
	if err != nil {
		return err
	}
	header.Name = name
	if info.IsDir() {
		header.Name += "/"
	}
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	_, copyErr := copyWithContext(ctx, writer, file)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
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
			if readErr == io.EOF {
				return total, nil
			}
			return total, readErr
		}
	}
}
