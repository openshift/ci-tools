package backporter

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/bugzilla"
	"k8s.io/utils/diff"
)

var (
	landingPage = fmt.Sprintf(htmlPageStart, "Home") + htmlPageEnd
)

var allTargetVersions = sets.NewString("4.0.0", "4.1.0", "4.4.z")

func TestGetLandingHandler(t *testing.T) {
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	handler := GetLandingHandler()
	handler.ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("error fetching landing page for bugzilla backporter tool!")
	}
	if resp := rr.Body.String(); resp != landingPage {
		t.Errorf("might not have changed the landingPage after modifying it in the backporter tool - Response differs from expected by: %s", diff.StringDiff(resp, landingPage))
	}
}

func TestGetBugHandler(t *testing.T) {
	fake := &bugzilla.Fake{}
	fake.Bugs = map[int]bugzilla.Bug{}
	fake.BugComments = map[int][]bugzilla.Comment{}
	bug1 := &bugzilla.BugCreate{
		AssignedTo: "UnitTest",
		Summary:    "Sample bug to test implementation of clones handler",
	}
	bug1ID, err := fake.CreateBug(bug1)
	if err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name       string
		params     map[string]int
		statusCode int
	}{
		{
			"good_params",
			map[string]int{
				"ID": bug1ID,
			},
			http.StatusOK,
		},
		{
			"no_params",
			map[string]int{},
			http.StatusBadRequest,
		},
		{
			"bad_params",
			map[string]int{
				"ID": 1000,
			},
			http.StatusNotFound,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", "/bug", nil)
			if err != nil {
				t.Fatal(err)
			}
			q := req.URL.Query()
			for k, v := range tc.params {
				q.Add(k, strconv.Itoa(v))
			}
			req.URL.RawQuery = q.Encode()
			rr := httptest.NewRecorder()
			handler := GetBugHandler(fake)
			handler.ServeHTTP(rr, req)
			if status := rr.Code; status != tc.statusCode {
				t.Errorf("testcase '%v' failed: getbug returned wrong status code - got %v, want %v", tc.name, status, tc.statusCode)
			}
		})
	}

}

func TestGetClonesHandler(t *testing.T) {
	fake := &bugzilla.Fake{}
	fake.Bugs = map[int]bugzilla.Bug{}
	fake.BugComments = map[int][]bugzilla.Comment{}

	toBeClonedCreate := &bugzilla.BugCreate{
		AssignedTo: "UnitTest",
		Summary:    "Sample bug to test implementation of clones handler",
	}
	toBeClonedID, err := fake.CreateBug(toBeClonedCreate)
	if err != nil {
		t.Fatalf("error creating bug: %v", err)
	}
	toBeCloned, err := fake.GetBug(toBeClonedID)
	if err != nil {
		t.Fatalf("error retreiving bug: %v", err)
	}
	cloneID, err := fake.CloneBug(toBeCloned)
	if err != nil {
		t.Fatalf("error while cloning bug: %v", err)
	}
	clone, err := fake.GetBug(cloneID)
	if err != nil {
		t.Fatalf("error retreiving clone: %v", err)
	}
	testCases := []struct {
		name       string
		params     map[string]int
		statusCode int
		data       interface{}
		tmplt      *template.Template
	}{
		{
			name: "valid_parameters",
			params: map[string]int{
				"ID": cloneID,
			},
			statusCode: http.StatusOK,
			data: ClonesTemplateData{
				Bug: clone,
				Clones: []*bugzilla.Bug{
					clone,
					toBeCloned,
				},
				Parent:       toBeCloned,
				PRs:          nil,
				CloneTargets: allTargetVersions.List(),
				NewCloneID:   0,
			},
			tmplt: clonesTemplate,
		},
		{
			name: "bad_params",
			params: map[string]int{
				"ID": 1000,
			},
			statusCode: http.StatusNotFound,
			data:       "unable to get get bug details",
			tmplt:      errorTemplate,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", "/clones", nil)
			if err != nil {
				t.Error(err)
			}
			q := req.URL.Query()
			for k, v := range tc.params {
				q.Add(k, strconv.Itoa(v))
			}
			req.URL.RawQuery = q.Encode()
			rr := httptest.NewRecorder()
			handler := GetClonesHandler(fake, allTargetVersions)
			handler.ServeHTTP(rr, req)
			if status := rr.Code; status != tc.statusCode {
				t.Errorf("testcase '%v' failed: getbug returned wrong status code - got %v, want %v", tc, status, tc.statusCode)
			}
			var buf bytes.Buffer
			if err := tc.tmplt.Execute(&buf, tc.data); err != nil {
				t.Fatalf("unable to render template: %v", err)
			}
			subPage := buf.String()
			var expResponse string
			if tc.statusCode == http.StatusOK {
				expResponse = fmt.Sprintf(htmlPageStart, "Clones") + subPage + htmlPageEnd
			} else {
				expResponse = fmt.Sprintf(htmlPageStart, "Not Found") + subPage + htmlPageEnd
			}
			if resp := rr.Body.String(); resp != expResponse {
				t.Errorf("response differs from expected by: %s", diff.StringDiff(resp, expResponse))
			}
		})
	}

}

func TestCreateCloneHandler(t *testing.T) {
	fake := &bugzilla.Fake{}
	fake.Bugs = map[int]bugzilla.Bug{}
	fake.BugComments = map[int][]bugzilla.Comment{}

	toBeClonedCreate := &bugzilla.BugCreate{
		AssignedTo: "UnitTest",
		Summary:    "Sample bug to test implementation of clones handler",
	}
	toBeClonedID, err := fake.CreateBug(toBeClonedCreate)
	if err != nil {
		t.Fatalf("error creating bug: %v", err)
	}
	clonedRelease := "4.4.z"
	prunedReleaseSet := sets.NewString(allTargetVersions.List()...)
	prunedReleaseSet.Delete(clonedRelease)

	toBeCloned, err := fake.GetBug(toBeClonedID)
	if err != nil {
		t.Fatalf("error getting bug details from Fake.")
	}
	expectedCloneID := toBeClonedID + 1
	expectedClone := *toBeCloned
	expectedClone.ID = expectedCloneID
	expectedClone.TargetRelease = []string{clonedRelease}
	testcases := []struct {
		name       string
		params     map[string]string
		statusCode int
		data       interface{}
		tmplt      *template.Template
		pageTitle  string
	}{
		{
			name: "Valid parameter proper response expected",
			params: map[string]string{
				"ID":      strconv.Itoa(toBeClonedID),
				"release": clonedRelease,
			},
			statusCode: http.StatusOK,
			data: ClonesTemplateData{
				Bug:          toBeCloned,
				Clones:       []*bugzilla.Bug{toBeCloned, &expectedClone},
				Parent:       toBeCloned,
				PRs:          nil,
				CloneTargets: prunedReleaseSet.List(),
				NewCloneID:   expectedCloneID,
			},
			tmplt:     clonesTemplate,
			pageTitle: "Clones",
		},
		{
			name: "Bad params- Non-existent bug ID ",
			params: map[string]string{
				"ID":      "1000",
				"release": "",
			},
			statusCode: http.StatusNotFound,
			data:       "unable to fetch bug details- Bug#1000",
			tmplt:      errorTemplate,
			pageTitle:  "Not Found",
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			formData := url.Values{}
			formData.Set("ID", tc.params["ID"])
			formData.Add("release", tc.params["release"])
			req, err := http.NewRequest("POST", "/clones/create", bytes.NewBufferString(formData.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded; param=value")
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			handler := CreateCloneHandler(fake, allTargetVersions)
			handler.ServeHTTP(rr, req)
			if status := rr.Code; status != tc.statusCode {
				t.Errorf("testcase '%v' failed: clonebug returned wrong status code - got %v, want %v", tc, status, tc.statusCode)
			}

			var buf bytes.Buffer
			pageStart := fmt.Sprintf(htmlPageStart, tc.pageTitle)

			if err := tc.tmplt.Execute(&buf, tc.data); err != nil {
				t.Fatalf("unable to render template: %v", err)
			}
			subPage := buf.String()
			expResponse := pageStart + subPage + htmlPageEnd

			if resp := rr.Body.String(); resp != expResponse {
				t.Errorf("response differs from expected by: %s", diff.StringDiff(resp, expResponse))
			}
		})
	}
}
