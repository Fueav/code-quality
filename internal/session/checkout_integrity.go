package session

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

type checkoutEntry struct {
	mode string
	oid  string
	path string
}

// VerifyTargetCheckout verifies the actual source bytes, not Git's cached dirty
// flags. Ordinary untracked build products are ignored; tracked files, the
// index, and detached HEAD must still describe the frozen target commit.
func (session NativeSession) VerifyTargetCheckout(ctx context.Context) error {
	root := session.RepositoryDirectory()
	if err := validateOwnerDirectory(root); err != nil {
		return fmt.Errorf("verify target checkout directory: %w", err)
	}
	if err := session.verifyTargetHead(ctx); err != nil {
		return err
	}
	tree, err := checkoutGitOutput(ctx, root, "ls-tree", "--full-tree", "-r", "-z", session.request.TargetCommit)
	if err != nil {
		return fmt.Errorf("read frozen target tree: %w", err)
	}
	entries, err := parseCheckoutEntries(tree, false)
	if err != nil {
		return err
	}
	index, err := checkoutGitOutput(ctx, root, "ls-files", "--stage", "-z")
	if err != nil {
		return fmt.Errorf("read checkout index: %w", err)
	}
	indexed, err := parseCheckoutEntries(index, true)
	if err != nil {
		return err
	}
	if len(entries) != len(indexed) {
		return errors.New("checkout index differs from the frozen target tree")
	}
	for i, entry := range entries {
		if entry != indexed[i] {
			return errors.New("checkout index differs from the frozen target tree")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := verifyCheckoutEntry(ctx, root, entry); err != nil {
			return fmt.Errorf("checkout source %q differs from the frozen target: %w", entry.path, err)
		}
	}
	// Detect HEAD or index changes during the content scan as well.
	finalIndex, err := checkoutGitOutput(ctx, root, "ls-files", "--stage", "-z")
	if err != nil || !bytes.Equal(index, finalIndex) {
		return errors.New("checkout index changed during target verification")
	}
	return session.verifyTargetHead(ctx)
}

func (session NativeSession) verifyTargetHead(ctx context.Context) error {
	head, err := checkoutGitOutput(ctx, session.RepositoryDirectory(), "rev-parse", "--verify", "HEAD")
	if err != nil || strings.TrimSpace(string(head)) != session.request.TargetCommit {
		return errors.New("checkout HEAD does not match the frozen target commit")
	}
	command := checkoutGitCommand(ctx, session.RepositoryDirectory(), "symbolic-ref", "-q", "HEAD")
	if err := command.Run(); err == nil {
		return errors.New("target checkout is not detached")
	} else {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
			return fmt.Errorf("verify detached target checkout: %w", err)
		}
	}
	return nil
}

func checkoutGitCommand(ctx context.Context, root string, arguments ...string) *exec.Cmd {
	args := []string{"--no-optional-locks", "--no-replace-objects", "-C", root}
	return exec.CommandContext(ctx, "git", append(args, arguments...)...)
}

func checkoutGitOutput(ctx context.Context, root string, arguments ...string) ([]byte, error) {
	return checkoutGitCommand(ctx, root, arguments...).Output()
}

func parseCheckoutEntries(raw []byte, index bool) ([]checkoutEntry, error) {
	entries := []checkoutEntry{}
	for _, record := range bytes.Split(raw, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		header, path, ok := bytes.Cut(record, []byte{'\t'})
		fields := strings.Fields(string(header))
		if !ok || len(fields) != 3 {
			return nil, errors.New("invalid checkout tree or index record")
		}
		entry := checkoutEntry{mode: fields[0], oid: fields[2], path: string(path)}
		if index {
			if fields[2] != "0" {
				return nil, errors.New("checkout index has unresolved entries")
			}
			entry.oid = fields[1]
		}
		if !gitCommitPattern.MatchString(entry.oid) || entry.path == "" || filepath.IsAbs(entry.path) ||
			filepath.Clean(entry.path) != entry.path || entry.path == ".." || strings.HasPrefix(entry.path, ".."+string(filepath.Separator)) {
			return nil, errors.New("invalid checkout tree or index entry")
		}
		switch entry.mode {
		case "100644", "100755", "120000", "160000":
		default:
			return nil, errors.New("unsupported checkout tree mode")
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func verifyCheckoutEntry(ctx context.Context, root string, entry checkoutEntry) error {
	path := root
	parts := strings.Split(entry.path, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("tracked parent is missing, replaced, or a symlink")
		}
	}
	path = filepath.Join(path, parts[len(parts)-1])
	before, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if entry.mode == "160000" {
		if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
			return errors.New("gitlink is not an uninitialized directory")
		}
		children, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		if len(children) != 0 {
			return errors.New("initialized or populated submodule cannot be verified as frozen source")
		}
		return nil
	}
	if entry.mode == "120000" {
		if before.Mode()&os.ModeSymlink == 0 {
			return errors.New("tracked symlink was replaced")
		}
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		after, err := os.Lstat(path)
		if err != nil || !os.SameFile(before, after) || before.ModTime() != after.ModTime() {
			return errors.New("tracked symlink changed while being verified")
		}
		return verifyBlob(ctx, entry.oid, int64(len(target)), strings.NewReader(target))
	}
	if !before.Mode().IsRegular() || (before.Mode().Perm()&0o100 != 0) != (entry.mode == "100755") {
		return errors.New("tracked file type or executable mode changed")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || before.Mode() != opened.Mode() || before.Size() != opened.Size() {
		return errors.New("tracked file changed before verification")
	}
	if err := verifyBlob(ctx, entry.oid, opened.Size(), file); err != nil {
		return err
	}
	after, statErr := file.Stat()
	current, pathErr := os.Lstat(path)
	if statErr != nil || pathErr != nil || !os.SameFile(opened, current) ||
		opened.Size() != after.Size() || opened.ModTime() != after.ModTime() || opened.Mode() != after.Mode() ||
		current.Size() != after.Size() || current.ModTime() != after.ModTime() || current.Mode() != after.Mode() {
		return errors.New("tracked file changed during verification")
	}
	return nil
}

func verifyBlob(ctx context.Context, want string, size int64, reader io.Reader) error {
	var digest hash.Hash
	switch len(want) {
	case 40:
		digest = sha1.New()
	case 64:
		digest = sha256.New()
	default:
		return errors.New("unsupported Git object hash")
	}
	fmt.Fprintf(digest, "blob %d\x00", size)
	read, err := io.Copy(digest, checkoutReader{ctx: ctx, reader: io.LimitReader(reader, size+1)})
	if err != nil {
		return err
	}
	if read != size || hex.EncodeToString(digest.Sum(nil)) != want {
		return errors.New("tracked file bytes differ from the target blob")
	}
	return nil
}

type checkoutReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader checkoutReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}
