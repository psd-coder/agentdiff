package app

import (
	"slices"
	"testing"
)

func TestPageStringRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		page page
		text string
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
		if got := tc.page.String(); got != tc.text {
			t.Errorf("%+v.String() = %q, want %q", tc.page, got, tc.text)
		}
		if got := parsePage(tc.text); got != tc.page {
			t.Errorf("parsePage(%q) = %+v, want %+v", tc.text, got, tc.page)
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
	navigation := &navigation{}
	for _, p := range pages {
		navigation.goTo(p)
	}
	return navigation
}

func TestBackAndForwardReturnThroughThePages(t *testing.T) {
	navigation := visit(pageA, pageB)
	navigation.back(noneGone)
	if navigation.page != pageA {
		t.Fatalf("back = %+v", navigation.page)
	}
	navigation.forward(noneGone)
	if navigation.page != pageB {
		t.Fatalf("forward = %+v", navigation.page)
	}
	navigation.back(noneGone)
	navigation.back(noneGone)
	navigation.back(noneGone)
	if navigation.page != overview {
		t.Errorf("back past the first page = %+v", navigation.page)
	}
}

func TestGoToDropsForward(t *testing.T) {
	navigation := visit(pageA, pageB)
	navigation.back(noneGone)
	navigation.goTo(pageC)
	navigation.forward(noneGone)
	if navigation.page != pageC {
		t.Errorf("forward after a new page = %+v", navigation.page)
	}
}

func TestGoToTheSamePageAddsNoEntry(t *testing.T) {
	navigation := visit(pageA, pageA)
	navigation.back(noneGone)
	if navigation.page != overview {
		t.Errorf("back = %+v", navigation.page)
	}
}

func TestBackSkipsGonePages(t *testing.T) {
	navigation := visit(pageA, pageB, pageC)
	navigation.back(goneOf(pageB))
	if navigation.page != pageA {
		t.Errorf("back = %+v", navigation.page)
	}
}

func TestForwardSkipsGonePages(t *testing.T) {
	navigation := visit(pageA, pageB, pageC)
	navigation.back(noneGone)
	navigation.back(noneGone)
	navigation.forward(goneOf(pageB))
	if navigation.page != pageC {
		t.Errorf("forward = %+v", navigation.page)
	}
}

func TestBackWithOnlyGonePagesStays(t *testing.T) {
	navigation := &navigation{page: pageB, history: []page{pageA}}
	navigation.back(goneOf(pageA))
	if navigation.page != pageB {
		t.Errorf("back = %+v", navigation.page)
	}
}

func TestReplaceAddsNoEntry(t *testing.T) {
	navigation := visit(pageA, pageB)
	navigation.back(noneGone)
	navigation.replace(pageC)
	navigation.back(noneGone)
	if navigation.page != overview {
		t.Errorf("back = %+v", navigation.page)
	}
	navigation.forward(noneGone)
	navigation.forward(noneGone)
	if navigation.page != pageB {
		t.Errorf("forward after replace = %+v", navigation.page)
	}
}

func TestForgetRemovesGonePagesFromBackAndForward(t *testing.T) {
	navigation := visit(pageC, pageA, pageB, pageC)
	navigation.back(noneGone)
	navigation.back(noneGone)
	navigation.forget(goneOf(pageC))
	if slices.Contains(navigation.history, pageC) || slices.Contains(navigation.ahead, pageC) {
		t.Errorf("history %+v, ahead %+v", navigation.history, navigation.ahead)
	}
	navigation.forward(noneGone)
	if navigation.page != pageB {
		t.Errorf("forward = %+v", navigation.page)
	}
}

func TestBackSkipsTheTopEqualToThePageAfterReplace(t *testing.T) {
	navigation := visit(pageA, pageC)
	navigation.replace(pageA)
	if previous, _ := navigation.prev(noneGone); previous != overview {
		t.Errorf("prev = %+v", previous)
	}
	navigation.back(noneGone)
	if navigation.page != overview {
		t.Errorf("back = %+v", navigation.page)
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
	navigation := &navigation{}
	for i := range maxHistory + 5 {
		navigation.goTo(page{kindWalk, string(rune('a' + i))})
	}
	if len(navigation.history) != maxHistory {
		t.Errorf("history holds %d pages", len(navigation.history))
	}
}
