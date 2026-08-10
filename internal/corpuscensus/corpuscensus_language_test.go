package corpuscensus

import "testing"

func TestDetectLanguageRecognizesEnglishProse(t *testing.T) {
	text := `We present a retrieval-augmented generation method for the task of
	question answering. Our approach combines a dense retriever with a
	generative reader, and we show that this improves accuracy on the
	benchmark. The results demonstrate that our method outperforms prior
	work on this dataset, and we discuss the implications for future
	research in this area.`
	got := DetectLanguage(text)
	if got.Language != "en" {
		t.Fatalf("got %+v, expected en", got)
	}
	if got.Method != "stopword_density_v1" {
		t.Fatalf("method=%q", got.Method)
	}
	if got.Confidence <= 0 {
		t.Fatalf("confidence=%v, expected positive", got.Confidence)
	}
}

func TestDetectLanguageReportsUnknownForShortText(t *testing.T) {
	got := DetectLanguage("Page one content here")
	if got.Language != "unknown" {
		t.Fatalf("got %+v, expected unknown for very short text", got)
	}
	if got.Confidence != 0 {
		t.Fatalf("confidence=%v, expected 0 for below-floor word count", got.Confidence)
	}
}

func TestDetectLanguageReportsUnknownForLowStopwordDensity(t *testing.T) {
	// 25+ tokens, none of them English stopwords -- e.g. a run of
	// identifiers/numbers/proper nouns, which is a realistic shape for a
	// badly-extracted or non-English page.
	text := "Xyzabc Qwerty Zxcvbn Poiuyt Lkjhgf Mnbvcx Asdfgh Rewq Tyui Opas Dfgh Jklz Xcvb Nmqw Erty Uiop Asdf Ghjk Lzxc Vbnm Qwer Tyui Opas Dfgh Jklz"
	got := DetectLanguage(text)
	if got.Language != "unknown" {
		t.Fatalf("got %+v, expected unknown for near-zero stopword density", got)
	}
}
