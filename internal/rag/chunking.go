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
	var builder strings.Builder
	builderStart := 0
	// The builder's joined Content always uses a normalized 2-byte "\n\n"
	// separator, even when the original body had extra blank lines
	// between two paragraphs — so builderStart+len(content) is not
	// necessarily this chunk's real end position in body. builderEnd
	// tracks that real position directly from each paragraph's own span
	// instead.
	builderEnd := 0

	flush := func() {
		if builder.Len() == 0 {
			return
		}
		content := builder.String()
		chunks = append(chunks, Chunk{
			VersionID: versionID, ChunkerID: chunkerID, ChunkerVersion: chunkerVersion,
			Ordinal: ordinal, StartOffset: builderStart, EndOffset: builderEnd,
			Content: content, ContentHash: ContentHash(content),
		})
		ordinal++
		builder.Reset()
	}

	for _, span := range paragraphs {
		paragraph := span.text
		paragraphStart := span.start

		if len(paragraph) > maxChunkBytes {
			flush()
			pieceStart := paragraphStart
			for _, piece := range splitBytes(paragraph, maxChunkBytes) {
				chunks = append(chunks, Chunk{
					VersionID: versionID, ChunkerID: chunkerID, ChunkerVersion: chunkerVersion,
					Ordinal: ordinal, StartOffset: pieceStart, EndOffset: pieceStart + len(piece),
					Content: piece, ContentHash: ContentHash(piece),
				})
				ordinal++
				pieceStart += len(piece)
			}
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
		builderEnd = paragraphStart + len(paragraph)
	}
	flush()
	if len(chunks) == 0 {
		return nil, fmt.Errorf("%w: chunking produced no chunks", ErrInvalidChunk)
	}
	return chunks, nil
}

// paragraphSpan pairs a paragraph's text with its real byte offset in the
// original body — strings.Split alone discards position information the
// moment it removes the "\n\n" separators, and a naive running total
// (offset += len(paragraph)) never accounts for those stripped separator
// bytes, drifting StartOffset/EndOffset away from where the paragraph
// actually sits in body from the second paragraph onward.
type paragraphSpan struct {
	text  string
	start int
}

func splitParagraphs(body string) []paragraphSpan {
	spans := make([]paragraphSpan, 0, 4)
	cursor := 0
	for {
		idx := strings.Index(body[cursor:], "\n\n")
		if idx == -1 {
			if piece := body[cursor:]; piece != "" {
				spans = append(spans, paragraphSpan{text: piece, start: cursor})
			}
			break
		}
		if piece := body[cursor : cursor+idx]; piece != "" {
			spans = append(spans, paragraphSpan{text: piece, start: cursor})
		}
		cursor += idx + 2
	}
	if len(spans) == 0 {
		spans = append(spans, paragraphSpan{text: body, start: 0})
	}
	return spans
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
