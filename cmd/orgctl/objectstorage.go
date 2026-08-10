package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	cfg, err := objectstorage.LoadConfig(os.LookupEnv)
	if err != nil {
		fmt.Fprintf(stderr, "load object storage config: %v\n", err)
		return nil, exitUsage
	}
	if !cfg.Enabled {
		fmt.Fprintln(stderr, "object storage is disabled (ORG_OBJECT_STORAGE_OCI_ENABLED is not true)")
		return nil, exitUsage
	}
	client, err := objectstorage.New(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "open object storage client: %v\n", err)
		return nil, exitInternal
	}
	return client, exitOK
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
