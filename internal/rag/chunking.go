package rag

import (
	"fmt"
	"strings"
)

const (
	DefaultChunkerID      = "rag-fixed-window"
	DefaultChunkerVersion = "v1"
	maxChunkBytes         = 1200
)

// ChunkBody deterministically splits normalized knowledge body text into
// bounded, versioned chunks. Same bytes + same chunker id/version always
// produce the same ordinals, offsets, and content hashes.
func ChunkBody(versionID, chunkerID, chunkerVersion, body string) ([]Chunk, error) {
	if strings.TrimSpace(versionID) == "" {
		return nil, fmt.Errorf("%w: chunking requires a version id", ErrInvalidChunk)
	}
	if strings.TrimSpace(chunkerID) == "" || strings.TrimSpace(chunkerVersion) == "" {
		return nil, fmt.Errorf("%w: chunking requires chunker id and version", ErrInvalidChunk)
	}
	if body == "" {
		return nil, fmt.Errorf("%w: cannot chunk empty body", ErrInvalidChunk)
	}
	paragraphs := splitParagraphs(body)
	chunks := make([]Chunk, 0, len(paragraphs))
	ordinal := 1
	offset := 0
	var builder strings.Builder
	builderStart := 0

	flush := func() {
		if builder.Len() == 0 {
			return
		}
		content := builder.String()
		chunks = append(chunks, Chunk{
			VersionID: versionID, ChunkerID: chunkerID, ChunkerVersion: chunkerVersion,
			Ordinal: ordinal, StartOffset: builderStart, EndOffset: builderStart + len(content),
			Content: content, ContentHash: ContentHash(content),
		})
		ordinal++
		builder.Reset()
	}

	for _, paragraph := range paragraphs {
		paragraphStart := offset
		offset += len(paragraph)

		if len(paragraph) > maxChunkBytes {
			flush()
			for _, piece := range splitBytes(paragraph, maxChunkBytes) {
				chunks = append(chunks, Chunk{
					VersionID: versionID, ChunkerID: chunkerID, ChunkerVersion: chunkerVersion,
					Ordinal: ordinal, StartOffset: paragraphStart, EndOffset: paragraphStart + len(piece),
					Content: piece, ContentHash: ContentHash(piece),
				})
				ordinal++
				paragraphStart += len(piece)
			}
			builderStart = offset
			continue
		}

		if builder.Len() == 0 {
			builderStart = paragraphStart
		}
		if builder.Len() > 0 && builder.Len()+len(paragraph)+2 > maxChunkBytes {
			flush()
			builderStart = paragraphStart
		}
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(paragraph)
	}
	flush()
	if len(chunks) == 0 {
		return nil, fmt.Errorf("%w: chunking produced no chunks", ErrInvalidChunk)
	}
	return chunks, nil
}

func splitParagraphs(body string) []string {
	raw := strings.Split(body, "\n\n")
	values := make([]string, 0, len(raw))
	for _, piece := range raw {
		if piece != "" {
			values = append(values, piece)
		}
	}
	if len(values) == 0 {
		values = append(values, body)
	}
	return values
}

func splitBytes(value string, size int) []string {
	runes := []rune(value)
	pieces := []string{}
	var builder strings.Builder
	for _, r := range runes {
		if builder.Len()+len(string(r)) > size && builder.Len() > 0 {
			pieces = append(pieces, builder.String())
			builder.Reset()
		}
		builder.WriteRune(r)
	}
	if builder.Len() > 0 {
		pieces = append(pieces, builder.String())
	}
	return pieces
}
