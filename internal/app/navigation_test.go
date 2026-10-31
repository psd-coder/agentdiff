package app

import (
	"slices"
	"testing"
)

func TestPageStringRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		p page
		s string
	}{
		{overview, "overview"},
		{page{kind: kindThreads}, "threads"},
		{page{kind: kindFeedback}, "feedback"},
		{page{kind: kindAll}, "all"},
		{page{kindWalk, "w3"}, "walk:w3"},
		{page{kindFile, "docs/a:b.md"}, "file:docs/a:b.md"},
		{page{kindGroup, "Tests: unit #2"}, "group:Tests: unit #2"},
		{page{kindArchived, "p1"}, "archived:p1"},
	} {
		if got := tc.p.String(); got != tc.s {
			t.Errorf("%+v.String() = %q, want %q", tc.p, got, tc.s)
		}
		if got := parsePage(tc.s); got != tc.p {
			t.Errorf("parsePage(%q) = %+v, want %+v", tc.s, got, tc.p)
		}
	}
}

func TestParsePageOfUnknownStringIsOverview(t *testing.T) {
	for _, s := range []string{"", "overviews", "walks:w1"} {
		if got := parsePage(s); got != overview {
			t.Errorf("parsePage(%q) = %+v", s, got)
		}
	}
}

var (
	pageA = page{kindFile, "a.go"}
	pageB = page{kindFile, "b.go"}
	pageC = page{kindWalk, "w1"}
)

func goneOf(pages ...page) func(page) bool {
	return func(p page) bool { return slices.Contains(pages, p) }
}

var noneGone = goneOf()

// visit opens the pages in turn from the Overview.
func visit(pages ...page) *navigation {
	n := &navigation{}
	for _, p := range pages {
		n.goTo(p)
	}
	return n
}

func TestBackAndForwardReturnThroughThePages(t *testing.T) {
	n := visit(pageA, pageB)
	n.back(noneGone)
	if n.page != pageA {
		t.Fatalf("back = %+v", n.page)
	}
	n.forward(noneGone)
	if n.page != pageB {
		t.Fatalf("forward = %+v", n.page)
	}
	n.back(noneGone)
	n.back(noneGone)
	n.back(noneGone)
	if n.page != overview {
		t.Errorf("back past the first page = %+v", n.page)
	}
}

func TestGoToDropsForward(t *testing.T) {
	n := visit(pageA, pageB)
	n.back(noneGone)
	n.goTo(pageC)
	n.forward(noneGone)
	if n.page != pageC {
		t.Errorf("forward after a new page = %+v", n.page)
	}
}

func TestGoToTheSamePageAddsNoEntry(t *testing.T) {
	n := visit(pageA, pageA)
	n.back(noneGone)
	if n.page != overview {
		t.Errorf("back = %+v", n.page)
	}
}

func TestBackSkipsGonePages(t *testing.T) {
	n := visit(pageA, pageB, pageC)
	n.back(goneOf(pageB))
	if n.page != pageA {
		t.Errorf("back = %+v", n.page)
	}
}

func TestForwardSkipsGonePages(t *testing.T) {
	n := visit(pageA, pageB, pageC)
	n.back(noneGone)
	n.back(noneGone)
	n.forward(goneOf(pageB))
	if n.page != pageC {
		t.Errorf("forward = %+v", n.page)
	}
}

func TestBackWithOnlyGonePagesStays(t *testing.T) {
	n := &navigation{page: pageB, history: []page{pageA}}
	n.back(goneOf(pageA))
	if n.page != pageB {
		t.Errorf("back = %+v", n.page)
	}
}

func TestReplaceAddsNoEntry(t *testing.T) {
	n := visit(pageA, pageB)
	n.back(noneGone)
	n.replace(pageC)
	n.back(noneGone)
	if n.page != overview {
		t.Errorf("back = %+v", n.page)
	}
	n.forward(noneGone)
	n.forward(noneGone)
	if n.page != pageB {
		t.Errorf("forward after replace = %+v", n.page)
	}
}

func TestForgetRemovesGonePagesFromBackAndForward(t *testing.T) {
	n := visit(pageC, pageA, pageB, pageC)
	n.back(noneGone)
	n.back(noneGone)
	n.forget(goneOf(pageC))
	if slices.Contains(n.history, pageC) || slices.Contains(n.ahead, pageC) {
		t.Errorf("history %+v, ahead %+v", n.history, n.ahead)
	}
	n.forward(noneGone)
	if n.page != pageB {
		t.Errorf("forward = %+v", n.page)
	}
}

func TestBackSkipsTheTopEqualToThePageAfterReplace(t *testing.T) {
	n := visit(pageA, pageC)
	n.replace(pageA)
	if p, _ := n.prev(noneGone); p != overview {
		t.Errorf("prev = %+v", p)
	}
	n.back(noneGone)
	if n.page != overview {
		t.Errorf("back = %+v", n.page)
	}
}

func TestPrevNamesThePageBackOpens(t *testing.T) {
	for _, tc := range []struct {
		name  string
		nav   *navigation
		gone  func(page) bool
		found bool
	}{
		{"plain", visit(pageA, pageB), noneGone, true},
		{"gone top", visit(pageA, pageB, pageC), goneOf(pageB), true},
		{"all gone", &navigation{page: pageC, history: []page{pageA, pageB}}, goneOf(pageA, pageB), false},
		{"empty", &navigation{}, noneGone, false},
	} {
		prev, ok := tc.nav.prev(tc.gone)
		before := tc.nav.page
		tc.nav.back(tc.gone)
		if ok != tc.found {
			t.Errorf("%s: prev found %v", tc.name, ok)
		}
		if ok && tc.nav.page != prev {
			t.Errorf("%s: prev %+v, back opened %+v", tc.name, prev, tc.nav.page)
		}
		if !ok && tc.nav.page != before {
			t.Errorf("%s: back moved to %+v without a previous page", tc.name, tc.nav.page)
		}
	}
}

func TestHistoryKeepsTheLastPages(t *testing.T) {
	n := &navigation{}
	for i := range maxHistory + 5 {
		n.goTo(page{kindWalk, string(rune('a' + i))})
	}
	if len(n.history) != maxHistory {
		t.Errorf("history holds %d pages", len(n.history))
	}
}
