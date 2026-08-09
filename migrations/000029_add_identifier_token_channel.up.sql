-- R29 phase 5: exact-match channel for numbers/codes, alongside FTS
-- (ts_rank) and the vector channel (rag_chunk_embeddings/
-- organizational_memory_embeddings). Embeddings do not replace lexical
-- precision for identifiers — a model can plausibly treat "20" and "2000"
-- as semantically close (both "a number in an error context"), which the
-- current FTS never does (ts_rank on "20" already correctly never matches
-- "2000", verified live). But FTS has its own separate failure: Postgres's
-- 'simple' tokenizer attaches a leading hyphen to a trailing number
-- ("error-20" tokenizes to the lexeme '-20', not '20'), so a document
-- written as "error-20" silently never matches a search for "error 20" or
-- "20" alone — a real, demonstrated false negative, not a hypothetical.
--
-- This channel sidesteps that tokenizer quirk entirely by extracting raw
-- digit runs with a plain regex, independent of Postgres's lexeme rules:
-- "error-20" and "error 20" both extract {20}; "error 2000" extracts
-- {2000} — a completely different, non-overlapping array element, so the
-- three-way test (error-20 <-> error 20 positive, error 20 -/-> error 2000
-- negative) holds by construction, not by tuning a similarity threshold.

CREATE FUNCTION extract_digit_runs(input text) RETURNS text[] LANGUAGE sql IMMUTABLE AS $$
    SELECT COALESCE(array_agg(m[1]), ARRAY[]::text[]) FROM regexp_matches(input, '\d+', 'g') AS m
$$;

ALTER TABLE rag_knowledge_chunks
    ADD COLUMN identifier_tokens TEXT[] GENERATED ALWAYS AS (extract_digit_runs(content)) STORED;

CREATE INDEX rag_knowledge_chunks_identifier_tokens_idx ON rag_knowledge_chunks USING GIN (identifier_tokens);

ALTER TABLE organizational_memory_versions
    ADD COLUMN identifier_tokens TEXT[] GENERATED ALWAYS AS (extract_digit_runs(problem || ' ' || correction)) STORED;

CREATE INDEX organizational_memory_versions_identifier_tokens_idx ON organizational_memory_versions USING GIN (identifier_tokens);
