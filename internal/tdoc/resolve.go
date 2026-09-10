package tdoc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// BaseURL is the root of the 3GPP FTP site.
const BaseURL = "https://www.3gpp.org/ftp/"

// ErrNotFound reports that a document could not be located: the TDoc
// number falls in no known meeting, or the meeting the caller named is
// unknown.
var ErrNotFound = errors.New("document not found")

// Document is a located meeting document: what to download and where it
// came from.
type Document struct {
	// ID is the cache key: the canonical TDoc number ("R1-2509715"), or for
	// a document named by path, the path itself.
	ID string
	// Path is the download location relative to BaseURL.
	Path string
	// Meeting is the meeting the document belongs to; zero when the
	// document was named by path.
	Meeting Meeting
	// Group is the owning group; zero when named by path.
	Group Group
}

// URL is the full download URL.
func (d Document) URL() string {
	segs := strings.Split(d.Path, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return BaseURL + strings.Join(segs, "/")
}

// Resolve locates a document. request is a TDoc number ("R1-2509715") or a
// path under the FTP site, either relative to /ftp/ or as a full URL
// ("tsg_ran/WG1_RL1/TSGR1_123/Report/Final_Minutes_report_RAN1#123_v100.zip").
// meeting optionally names the meeting a TDoc number belongs to (see
// FindMeeting); without it the number is matched against each meeting's
// TDoc range, which is how a number from a report or an LS is looked up.
func Resolve(ctx context.Context, client *http.Client, request, meeting string, useCache bool) (Document, error) {
	if p, ok := documentPath(request); ok {
		return Document{ID: p, Path: p}, nil
	}

	id, err := ParseID(request)
	if err != nil {
		return Document{}, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	g := id.Group()

	meetings, err := FetchMeetings(ctx, client, g, useCache)
	if err != nil {
		return Document{}, err
	}

	var m Meeting
	if meeting != "" {
		if strings.Contains(meeting, "/") {
			// An explicit folder: trust it, no listing needed.
			dir, ok := documentPath(strings.TrimSuffix(meeting, "/"))
			if !ok {
				return Document{}, fmt.Errorf("%w: invalid meeting folder %q", ErrNotFound, meeting)
			}
			m = Meeting{Dir: dir, DocsDir: dir + "/Docs"}
			if strings.EqualFold(path.Base(dir), "docs") {
				m.Dir, m.DocsDir = path.Dir(dir), dir
			}
		} else {
			found, ok := FindMeeting(meetings, meeting)
			if !ok {
				return Document{}, fmt.Errorf("%w: %s has no meeting %q", ErrNotFound, g.Name, meeting)
			}
			m = found
		}
	} else {
		found, ok := meetingForID(meetings, id)
		if !ok {
			return Document{}, fmt.Errorf("%w: no %s meeting lists %s in its TDoc range; pass the meeting (e.g. %q) if you know it", ErrNotFound, g.Name, id, exampleMeeting(meetings))
		}
		m = found
	}
	if m.DocsDir == "" {
		return Document{}, fmt.Errorf("%w: meeting %s has no document folder on the FTP site yet", ErrNotFound, m.Code)
	}
	return Document{
		ID:      id.String(),
		Path:    m.DocsDir + "/" + id.String() + ".zip",
		Meeting: m,
		Group:   g,
	}, nil
}

// meetingForID finds the meeting whose TDoc range holds id. Meetings are
// listed newest first, so when ranges overlap — a number re-used across an
// ad-hoc and its parent meeting — the most recent wins.
func meetingForID(meetings []Meeting, id ID) (Meeting, bool) {
	for _, m := range meetings {
		if m.Holds(id) {
			return m, true
		}
	}
	return Meeting{}, false
}

func exampleMeeting(meetings []Meeting) string {
	for _, m := range meetings {
		if m.Dir != "" {
			return m.Code
		}
	}
	return "R1-123"
}

// documentPath recognizes a request that names a file or folder on the FTP
// site rather than a TDoc number, and normalizes it to a path relative to
// /ftp/. Only paths under the site are accepted, without ".." components.
func documentPath(request string) (string, bool) {
	p := strings.ReplaceAll(strings.TrimSpace(request), `\`, "/")
	if !strings.Contains(p, "/") {
		return "", false
	}
	// A URL — including a protocol-relative "//host/..." one, which parses
	// with an empty scheme — must name the 3GPP site, or a request for a
	// foreign file would silently resolve to a different file under BaseURL.
	if u, err := url.Parse(p); err == nil && (u.Scheme != "" || u.Host != "") {
		if !strings.EqualFold(u.Host, "www.3gpp.org") && !strings.EqualFold(u.Host, "3gpp.org") {
			return "", false
		}
		// 3GPP file names carry "#" ("Final_Minutes_report_RAN1#123_v100.zip"),
		// which a URL parser reads as a fragment: put it back.
		p = u.Path
		if u.Fragment != "" {
			p += "#" + u.Fragment
		}
	} else if unescaped, err := url.PathUnescape(p); err == nil {
		// A bare path is decoded too, so the traversal check below sees
		// the same form for both inputs ("%2e%2e" is ".."), and a pasted
		// listing link ("...RAN1%23123_v100.zip") names the real file.
		p = unescaped
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", false
		}
	}
	if i := strings.Index(strings.ToLower(p), "/ftp/"); i >= 0 {
		p = p[i+len("/ftp/"):]
	} else if strings.HasPrefix(strings.ToLower(p), "ftp/") {
		p = p[len("ftp/"):]
	}
	p = strings.Trim(path.Clean("/"+p), "/")
	if p == "" || p == "." {
		return "", false
	}
	return p, true
}

// CanonicalID returns the cache key a request resolves to without touching
// the network: the canonical TDoc number, or the normalized path. ok is
// false when the request is neither.
func CanonicalID(request string) (string, bool) {
	if p, ok := documentPath(request); ok {
		return p, true
	}
	id, err := ParseID(request)
	if err != nil {
		return "", false
	}
	return id.String(), true
}
