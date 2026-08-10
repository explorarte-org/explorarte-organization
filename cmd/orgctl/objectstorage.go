package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/objectstorage"
)

func openObjectStorageClient(stderr io.Writer) (*objectstorage.Client, int) {
	client, err := newObjectStorageClient()
	if err != nil {
		fmt.Fprintln(stderr, err)
		if errors.Is(err, errObjectStorageDisabled) {
			return nil, exitUsage
		}
		return nil, exitInternal
	}
	return client, exitOK
}

var errObjectStorageDisabled = errors.New("object storage is disabled (ORG_OBJECT_STORAGE_OCI_ENABLED is not true)")

// newObjectStorageClient is the plain-error core openObjectStorageClient
// wraps for CLI subcommands (which report via stderr/exit code) -- reused
// directly by callers deeper in the stack (e.g. runRAGIngestPDF) that
// don't have a stderr writer of their own to report through.
func newObjectStorageClient() (*objectstorage.Client, error) {
	cfg, err := objectstorage.LoadConfig(os.LookupEnv)
	if err != nil {
		return nil, fmt.Errorf("load object storage config: %w", err)
	}
	if !cfg.Enabled {
		return nil, errObjectStorageDisabled
	}
	client, err := objectstorage.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("open object storage client: %w", err)
	}
	return client, nil
}

func runObjectStorage(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: orgctl objectstorage <list|get|put|seed> ...")
		return exitUsage
	}
	client, code := openObjectStorageClient(stderr)
	if code != exitOK {
		return code
	}
	ctx := context.Background()
	switch args[0] {
	case "list":
		flags := flag.NewFlagSet("objectstorage list", flag.ContinueOnError)
		flags.SetOutput(stderr)
		prefix := flags.String("prefix", "", "object key prefix")
		if flags.Parse(args[1:]) != nil {
			return exitUsage
		}
		objects, err := client.ListObjects(ctx, *prefix)
		if err != nil {
			fmt.Fprintf(stderr, "list objects: %v\n", err)
			return exitInternal
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(map[string]any{"count": len(objects), "objects": objects})
		return exitOK
	case "get":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: orgctl objectstorage get <object-name>")
			return exitUsage
		}
		body, err := client.GetObject(ctx, args[1])
		if err != nil {
			fmt.Fprintf(stderr, "get object: %v\n", err)
			return exitInternal
		}
		_, _ = stdout.Write(body)
		return exitOK
	case "put":
		flags := flag.NewFlagSet("objectstorage put", flag.ContinueOnError)
		flags.SetOutput(stderr)
		file := flags.String("file", "", "local file to upload")
		object := flags.String("object", "", "destination object name")
		contentType := flags.String("content-type", "", "content type (guessed from extension if omitted)")
		if flags.Parse(args[1:]) != nil {
			return exitUsage
		}
		if *file == "" || *object == "" {
			fmt.Fprintln(stderr, "usage: orgctl objectstorage put --file <path> --object <key> [--content-type <type>]")
			return exitUsage
		}
		body, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintf(stderr, "read file: %v\n", err)
			return exitInvalid
		}
		ct := *contentType
		if ct == "" {
			ct = guessContentType(*file)
		}
		if err := client.PutObject(ctx, *object, body, ct); err != nil {
			fmt.Fprintf(stderr, "put object: %v\n", err)
			return exitInternal
		}
		sum := sha256.Sum256(body)
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(map[string]any{
			"object": *object, "bytes": len(body), "sha256": hex.EncodeToString(sum[:]),
		})
		return exitOK
	case "delete":
		flags := flag.NewFlagSet("objectstorage delete", flag.ContinueOnError)
		flags.SetOutput(stderr)
		prefix := flags.String("prefix", "", "delete every object under this prefix instead of a single name")
		all := flags.Bool("all", false, "delete every object in the bucket (prefix is ignored)")
		if err := flags.Parse(args[1:]); err != nil {
			return exitUsage
		}
		if *all || *prefix != "" {
			objects, err := client.ListObjects(ctx, *prefix)
			if err != nil {
				fmt.Fprintf(stderr, "list objects: %v\n", err)
				return exitInternal
			}
			deleted := 0
			for _, object := range objects {
				if err := client.DeleteObject(ctx, object.Name); err != nil {
					fmt.Fprintf(stderr, "delete %s: %v\n", object.Name, err)
					return exitInternal
				}
				deleted++
				fmt.Fprintf(stderr, "deleted %s (%d/%d)\n", object.Name, deleted, len(objects))
			}
			fmt.Fprintf(stdout, "{\"prefix\":%q,\"deleted_count\":%d}\n", *prefix, deleted)
			return exitOK
		}
		rest := flags.Args()
		if len(rest) != 1 {
			fmt.Fprintln(stderr, "usage: orgctl objectstorage delete <object-name> | delete --prefix <prefix>")
			return exitUsage
		}
		if err := client.DeleteObject(ctx, rest[0]); err != nil {
			fmt.Fprintf(stderr, "delete object: %v\n", err)
			return exitInternal
		}
		fmt.Fprintf(stdout, "{\"deleted\":%q}\n", rest[0])
		return exitOK
	case "seed":
		return objectStorageSeed(ctx, client, args[1:], stdout, stderr)
	case "whoami":
		body, headers, err := client.DebugRequestNamespace(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "whoami: %v\n", err)
			return exitInternal
		}
		fmt.Fprintf(stdout, "namespace response: %s\nheaders: %v\n", string(body), headers)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown objectstorage command %q\n", args[0])
		return exitUsage
	}
}

// seedManifestEntry mirrors, in a minimal form, the manifest/provenance
// record the ingestion pipeline's later phases (corpus census, dedup) will
// consume: one line per uploaded object, with its content hash for dedup
// and its local source path for provenance.
type seedManifestEntry struct {
	SourcePath string `json:"source_path"`
	ObjectName string `json:"object_name"`
	Bytes      int    `json:"bytes"`
	SHA256     string `json:"sha256"`
}

// objectStorageSeed walks --local-dir and uploads every regular file into
// the bucket under raw/<object-prefix>/<relative path>, then writes a JSON
// manifest of what it uploaded (source path, object name, size, sha256)
// to manifests/<manifest-name> in the same bucket. This is the initial
// corpus load into raw/ -- phases 2-4 (census, manifest schema, real dedup
// against prior manifests) build on top of what this records, they don't
// replace it.
func objectStorageSeed(ctx context.Context, client *objectstorage.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("objectstorage seed", flag.ContinueOnError)
	flags.SetOutput(stderr)
	localDir := flags.String("local-dir", "", "local directory to upload recursively")
	objectPrefix := flags.String("object-prefix", "", "destination prefix under raw/ (e.g. software-architecture)")
	manifestName := flags.String("manifest", "", "manifest object name under manifests/")
	dryRun := flags.Bool("dry-run", false, "list what would be uploaded without uploading")
	if flags.Parse(args) != nil {
		return exitUsage
	}
	if *localDir == "" || *objectPrefix == "" {
		fmt.Fprintln(stderr, "usage: orgctl objectstorage seed --local-dir <path> --object-prefix <raw-subdir> [--manifest <name>] [--dry-run]")
		return exitUsage
	}

	var entries []seedManifestEntry
	err := filepath.WalkDir(*localDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(*localDir, path)
		if err != nil {
			return err
		}
		objectName := "raw/" + *objectPrefix + "/" + filepath.ToSlash(rel)

		body, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		sum := sha256.Sum256(body)
		entry := seedManifestEntry{
			SourcePath: path, ObjectName: objectName, Bytes: len(body), SHA256: hex.EncodeToString(sum[:]),
		}
		if !*dryRun {
			if err := client.PutObject(ctx, objectName, body, guessContentType(path)); err != nil {
				return fmt.Errorf("upload %s: %w", objectName, err)
			}
		}
		entries = append(entries, entry)
		fmt.Fprintf(stderr, "%s -> %s (%d bytes)\n", path, objectName, len(body))
		return nil
	})
	if err != nil {
		fmt.Fprintf(stderr, "seed: %v\n", err)
		return exitInternal
	}

	if *manifestName != "" && !*dryRun {
		manifestBody, err := json.MarshalIndent(map[string]any{
			"object_prefix": *objectPrefix, "count": len(entries), "entries": entries,
		}, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "encode manifest: %v\n", err)
			return exitInternal
		}
		if err := client.PutObject(ctx, "manifests/"+*manifestName, manifestBody, "application/json"); err != nil {
			fmt.Fprintf(stderr, "upload manifest: %v\n", err)
			return exitInternal
		}
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(map[string]any{"uploaded": len(entries), "dry_run": *dryRun, "entries": entries})
	return exitOK
}

func guessContentType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	switch ext {
	case ".md":
		return "text/markdown; charset=utf-8"
	case ".epub":
		return "application/epub+zip"
	default:
		return "application/octet-stream"
	}
}
