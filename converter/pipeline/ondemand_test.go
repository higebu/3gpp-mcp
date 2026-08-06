package pipeline

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeArchive serves a directory listing for TS 23.501 with a spread of
// versions, so version resolution can be exercised without the network.
func fakeArchive(t *testing.T) *http.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/ftp/Specs/archive/23_series/23.501/", func(w http.ResponseWriter, _ *http.Request) {
		for _, name := range []string{"23501-i60.zip", "23501-j50.zip", "23501-k20.zip", "23501-k00.zip"} {
			fmt.Fprintf(w, `<a href="%s">%s</a>`+"\n", name, name)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}
}

func TestListVersions(t *testing.T) {
	versions, err := ListVersions(context.Background(), fakeArchive(t), "TS 23.501", false)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	want := []string{"k20", "k00", "j50", "i60"}
	if len(versions) != len(want) {
		t.Fatalf("got %d versions, want %d", len(versions), len(want))
	}
	for i, w := range want {
		if versions[i].Version != w {
			t.Errorf("versions[%d] = %q, want %q (newest first)", i, versions[i].Version, w)
		}
	}
}

func TestResolveVersion(t *testing.T) {
	client := fakeArchive(t)

	tests := []struct {
		name    string
		request string
		want    string
		wantErr bool
	}{
		{name: "dotted", request: "18.6.0", want: "i60"},
		{name: "archive token", request: "i60", want: "i60"},
		{name: "uppercase token", request: "I60", want: "i60"},
		{name: "v prefix", request: "v19.5.0", want: "j50"},
		{name: "latest", request: "latest", want: "k20"},
		{name: "empty means latest", request: "", want: "k20"},
		{name: "release selector picks newest in release", request: "Rel-20", want: "k20"},
		{name: "bare release number", request: "18", want: "i60"},
		{name: "unknown version", request: "17.1.0", wantErr: true},
		{name: "unknown release", request: "Rel-5", wantErr: true},
		{name: "malformed", request: "18.6", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sv, available, err := ResolveVersion(context.Background(), client, "TS 23.501", tt.request, false)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ResolveVersion(%q) = %v, want error", tt.request, sv)
				}
				// Callers show the user what does exist, so the list must survive.
				if len(available) == 0 {
					t.Error("expected the available versions alongside the error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveVersion(%q): %v", tt.request, err)
			}
			if sv.Version != tt.want {
				t.Errorf("ResolveVersion(%q) = %q, want %q", tt.request, sv.Version, tt.want)
			}
		})
	}
}

func TestResolveVersionUnknownSpec(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	if _, _, err := ResolveVersion(context.Background(), client, "TS 99.999", "18.6.0", false); err == nil {
		t.Error("expected an error for a spec that is not in the archive")
	}
}

func TestReleaseRequest(t *testing.T) {
	tests := []struct {
		in   string
		want int
		ok   bool
	}{
		{"18", 18, true},
		{"Rel-18", 18, true},
		{"rel-18", 18, true},
		{"R18", 18, true},
		{"18.6.0", 0, false},
		{"i60", 0, false},
		{"", 0, false},
		{"Rel-", 0, false},
		// A bare three-digit string is an archive token (9.2.0), never a
		// release selector.
		{"920", 0, false},
		{"100", 0, false},
		{"Rel-920", 920, true},
	}
	for _, tt := range tests {
		got, ok := releaseRequest(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("releaseRequest(%q) = %d, %v; want %d, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

// makeDocxWithImage builds a minimal .docx whose only paragraph embeds
// word/media/image1.png with the given bytes.
func makeDocxWithImage(t *testing.T, pngData []byte) []byte {
	t.Helper()
	const contentTypes = `<?xml version="1.0"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="xml" ContentType="application/xml"/>
<Default Extension="png" ContentType="image/png"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`
	const rels = `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`
	const docRels = `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId5" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>
</Relationships>`
	const doc = `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<w:body>
<w:p><w:pPr><w:pStyle w:val="Heading 1"/></w:pPr><w:r><w:t>5 Test</w:t></w:r></w:p>
<w:p><w:r><w:drawing><wp:inline xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing">` +
		`<a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><a:graphicData>` +
		`<pic:pic xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:blipFill>` +
		`<a:blip r:embed="rId5"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>
</w:body>
</w:document>`
	return makeZipWithFiles(t, map[string][]byte{
		"[Content_Types].xml":          []byte(contentTypes),
		"_rels/.rels":                  []byte(rels),
		"word/document.xml":            []byte(doc),
		"word/_rels/document.xml.rels": []byte(docRels),
		"word/media/image1.png":        pngData,
	})
}

// TestFetchVersionImages covers the lazy image download: images come out of
// the archive ZIP, and parts of a multi-file spec merge last-write-wins by
// name, as the section merge does.
func TestFetchVersionImages(t *testing.T) {
	archive := makeZipWithFiles(t, map[string][]byte{
		"23501-i60_s01.docx": makeDocxWithImage(t, []byte("png-part-one")),
		"23501-i60_s02.docx": makeDocxWithImage(t, []byte("png-part-two")),
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/ftp/Specs/archive/23_series/23.501/23501-i60.zip", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	sv := ParseSpecEntry("23_series/23.501/23501-i60.zip")
	if sv == nil {
		t.Fatal("ParseSpecEntry returned nil")
	}
	images, err := FetchVersionImages(context.Background(), client, sv, 0)
	if err != nil {
		t.Fatalf("FetchVersionImages: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("got %d images, want the parts merged into 1: %+v", len(images), images)
	}
	img := images[0]
	if img.Name != "image1.png" || img.MIMEType != "image/png" || !img.LLMReadable {
		t.Errorf("image = %+v, want a readable image1.png", img)
	}
	if string(img.Data) != "png-part-two" {
		t.Errorf("Data = %q, want the later part to win", img.Data)
	}
}

// TestFetchVersionImagesSkipsCorruptPart checks that one unparsable part does
// not lose the images of the others.
func TestFetchVersionImagesSkipsCorruptPart(t *testing.T) {
	archive := makeZipWithFiles(t, map[string][]byte{
		"23501-i60_s01.docx": []byte("not a zip"),
		"23501-i60_s02.docx": makeDocxWithImage(t, []byte("png-bytes")),
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/ftp/Specs/archive/23_series/23.501/23501-i60.zip", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	sv := ParseSpecEntry("23_series/23.501/23501-i60.zip")
	if sv == nil {
		t.Fatal("ParseSpecEntry returned nil")
	}
	images, err := FetchVersionImages(context.Background(), client, sv, 0)
	if err != nil {
		t.Fatalf("FetchVersionImages: %v", err)
	}
	if len(images) != 1 || images[0].Name != "image1.png" {
		t.Fatalf("images = %+v, want the valid part's image1.png", images)
	}
}

// TestFetchVersionImagesAllPartsCorrupt checks that an entry whose every part
// fails to parse errors out instead of reporting a figure-less version, which
// would be cached permanently.
func TestFetchVersionImagesAllPartsCorrupt(t *testing.T) {
	archive := makeZipWithFiles(t, map[string][]byte{
		"23501-i60_s01.docx": []byte("not a zip"),
		"23501-i60_s02.docx": []byte("also not a zip"),
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/ftp/Specs/archive/23_series/23.501/23501-i60.zip", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	sv := ParseSpecEntry("23_series/23.501/23501-i60.zip")
	if sv == nil {
		t.Fatal("ParseSpecEntry returned nil")
	}
	if _, err := FetchVersionImages(context.Background(), client, sv, 0); !errors.Is(err, ErrNoDocx) {
		t.Fatalf("FetchVersionImages = %v, want ErrNoDocx when nothing parses", err)
	}
}

// TestFetchVersionImagesNoDocuments checks the error for an archive entry that
// contains no documents at all.
func TestFetchVersionImagesNoDocuments(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ftp/Specs/archive/23_series/23.501/23501-i60.zip", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(makeRawZipEntry(t, "readme.txt", []byte("nothing here")))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	sv := ParseSpecEntry("23_series/23.501/23501-i60.zip")
	if sv == nil {
		t.Fatal("ParseSpecEntry returned nil")
	}
	if _, err := FetchVersionImages(context.Background(), client, sv, 0); !errors.Is(err, ErrNoDocx) {
		t.Fatalf("FetchVersionImages = %v, want ErrNoDocx", err)
	}
}

// TestFetchVersionImagesDownloadError checks that a failed download propagates.
func TestFetchVersionImagesDownloadError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	sv := ParseSpecEntry("23_series/23.501/23501-i60.zip")
	if sv == nil {
		t.Fatal("ParseSpecEntry returned nil")
	}
	if _, err := FetchVersionImages(context.Background(), client, sv, 0); err == nil {
		t.Fatal("FetchVersionImages should fail when the download fails")
	}
}

// TestFetchVersionImagesDocOnly checks that a legacy .doc-only version reports
// the same guidance as the section fetch.
func TestFetchVersionImagesDocOnly(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ftp/Specs/archive/23_series/23.501/23501-300.zip", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(makeRawZipEntry(t, "23501-300.doc", []byte("legacy")))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	sv := ParseSpecEntry("23_series/23.501/23501-300.zip")
	if sv == nil {
		t.Fatal("ParseSpecEntry returned nil")
	}
	if _, err := FetchVersionImages(context.Background(), client, sv, 0); !errors.Is(err, ErrNoDocx) {
		t.Fatalf("FetchVersionImages = %v, want ErrNoDocx", err)
	}
}

// TestFetchVersionTRDocType verifies that an on-demand fetch labels a
// Technical Report "TR " even when the type is only known to the cover file,
// and that every section carries that ID (#110).
func TestFetchVersionTRDocType(t *testing.T) {
	partDocx := makeMinimalDocx(t,
		`<w:p><w:pPr><w:pStyle w:val="Heading 1"/></w:pPr><w:r><w:t>5 Definitions</w:t></w:r></w:p>`+
			`<w:p><w:r><w:t>Vocabulary body text.</w:t></w:r></w:p>`)
	coverDocx := makeMinimalDocx(t,
		`<w:p><w:pPr><w:pStyle w:val="ZA"/></w:pPr><w:r><w:t>3GPP TR 21.905 V17.2.0 (2022-03)</w:t></w:r></w:p>`)
	archive := makeZipWithFiles(t, map[string][]byte{
		"21905-h20_s01.docx":   partDocx,
		"21905-h20_cover.docx": coverDocx,
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/ftp/Specs/archive/21_series/21.905/21905-h20.zip", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	sv := ParseSpecEntry("21_series/21.905/21905-h20.zip")
	if sv == nil {
		t.Fatal("ParseSpecEntry returned nil")
	}
	spec, sections, err := FetchVersion(context.Background(), client, sv, 0)
	if err != nil {
		t.Fatalf("FetchVersion: %v", err)
	}
	if spec.ID != "TR 21.905" {
		t.Errorf("spec ID = %q, want %q", spec.ID, "TR 21.905")
	}
	for _, s := range sections {
		if s.SpecID != "TR 21.905" {
			t.Errorf("section %s SpecID = %q, want %q", s.Number, s.SpecID, "TR 21.905")
		}
	}
}

// TestFetchVersionDocOnly checks the error a legacy .doc-only version gives,
// since old versions hit this far more often than recent ones.
func TestFetchVersionDocOnly(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ftp/Specs/archive/23_series/23.501/23501-300.zip", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(makeRawZipEntry(t, "23501-300.doc", []byte("legacy")))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	sv := ParseSpecEntry("23_series/23.501/23501-300.zip")
	if sv == nil {
		t.Fatal("ParseSpecEntry returned nil")
	}
	_, _, err := FetchVersion(context.Background(), client, sv, 0)
	if !errors.Is(err, ErrNoDocx) {
		t.Fatalf("FetchVersion = %v, want ErrNoDocx", err)
	}
	if !strings.Contains(err.Error(), "LibreOffice") {
		t.Errorf("error should explain why a .doc-only version fails, got: %v", err)
	}
}
