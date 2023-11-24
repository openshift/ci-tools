package backporter

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"testing"

	"k8s.io/test-infra/prow/bugzilla"
	"k8s.io/test-infra/prow/metrics"
	"k8s.io/utils/diff"
)

var (
	landingPage = fmt.Sprintf(htmlPageStart, "Home") + helpTemplateConstructor + htmlPageEnd
)

var fakebzbpMetrics = metrics.NewMetrics("fakebzbp")

func TestGetLandingHandler(t *testing.T) {
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	handler := GetLandingHandler(fakebzbpMetrics)
	handler.ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("error fetching landing page for bugzilla backporter tool!")
	}
	if resp := rr.Body.String(); resp != landingPage {
		t.Errorf("might not have changed the landingPage after modifying it in the backporter tool - Response differs from expected by: %s", diff.StringDiff(resp, landingPage))
	}
}

func TestCompareTargetReleaseSort(t *testing.T) {
	assess := func(unsorted []string, expected []string) {
		err := SortTargetReleases(unsorted, true)
		if err != nil {
			t.Errorf("Error sorting target release list %v: %v", unsorted, err)
			return
		}
		if !reflect.DeepEqual(unsorted, expected) {
			t.Errorf("Semantic sort of %v did not reach expected %v", unsorted, expected)
		}
	}

	assess([]string{"4.2.z"}, []string{"4.2.z"})
	assess([]string{"4.3.z", "4.10.0", "4.10.z", "4.1.0", "4.2.0", "4.11.0", "4.1.z", "4.2.z"}, []string{"4.1.0", "4.1.z", "4.2.0", "4.2.z", "4.3.z", "4.10.0", "4.10.z", "4.11.0"})
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
			handler := GetBugHandler(fake, fakebzbpMetrics)
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
	if err := fake.UpdateBug(toBeClonedID, bugzilla.BugUpdate{
		TargetRelease: []string{"4.10.0"},
	}); err != nil {
		t.Fatalf("error while updating bug: %v", err)
	}
	toBeCloned, err := fake.GetBug(toBeClonedID)
	if err != nil {
		t.Fatalf("error retreiving bug: %v", err)
	}
	cloneID, err := fake.CloneBug(toBeCloned)
	if err != nil {
		t.Fatalf("error while cloning bug: %v", err)
	}
	if err := fake.UpdateBug(cloneID, bugzilla.BugUpdate{
		TargetRelease: []string{"4.9.z"},
	}); err != nil {
		t.Fatalf("error while updating bug: %v", err)
	}
	clone, err := fake.GetBug(cloneID)
	if err != nil {
		t.Fatalf("error retreiving clone: %v", err)
	}
	allTargetVersions := []string{"4.9.z", "4.10.0", "4.1.z", "4.2.z", "4.3.z", "4.4.z", "4.5.z", "4.6.z", "4.7.z", "4.8.z"}
	// backporter logic relies on allTargetVersions being sorted
	err = SortTargetReleases(allTargetVersions, true)
	if err != nil {
		t.Fatalf("Unable to sort target release slice")
	}
	testCases := []struct {
		name              string
		params            map[string]int
		statusCode        int
		data              interface{}
		tmplt             *template.Template
		allTargetVersions []string
	}{
		{
			name: "valid_parameters",
			params: map[string]int{
				"ID": cloneID,
			},
			statusCode: http.StatusOK,
			data: ClonesTemplateData{
				clone,
				[]*bugzilla.Bug{
					clone,
					toBeCloned,
				},
				toBeCloned,
				nil,
				[]string{"4.8.z", "4.7.z", "4.6.z", "4.5.z", "4.4.z", "4.3.z", "4.2.z", "4.1.z"},
				[]string{},
				[]string{},
				&dependenceNode{toBeClonedID, toBeCloned.TargetRelease[0], []*dependenceNode{{cloneID, clone.TargetRelease[0], nil}}},
			},
			tmplt:             clonesTemplate,
			allTargetVersions: allTargetVersions,
		},
		{
			name: "bad_params",
			params: map[string]int{
				"ID": 1000,
			},
			statusCode:        http.StatusNotFound,
			data:              "unable to get bug details",
			tmplt:             errorTemplate,
			allTargetVersions: []string{"4.4.z", "4.5.z", "4.6.0"},
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
			handler := GetClonesHandler(fake, tc.allTargetVersions, fakebzbpMetrics)
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
	clonedRelease := "4.8.z"
	intermediateRelease := "4.9.z"
	originalTargetRelease := "4.10.0"
	if err := fake.UpdateBug(toBeClonedID, bugzilla.BugUpdate{
		TargetRelease: []string{originalTargetRelease},
	}); err != nil {
		t.Fatalf("error while updating bug: %v", err)
	}

	toBeCloned, err := fake.GetBug(toBeClonedID)
	if err != nil {
		t.Fatalf("error getting bug details from Fake.")
	}
	toBeCloned.TargetRelease = []string{"4.10.0"}
	intermediateClone := *toBeCloned
	intermediateClone.ID = toBeClonedID + 1
	intermediateClone.TargetRelease = []string{intermediateRelease}

	expectedClone := *toBeCloned
	expectedClone.ID = toBeClonedID + 2
	expectedClone.TargetRelease = []string{clonedRelease}
	testcases := []struct {
		name              string
		params            map[string]string
		statusCode        int
		data              interface{}
		tmplt             *template.Template
		pageTitle         string
		existingBugs      map[int]bugzilla.Bug
		allTargetVersions []string
	}{
		{
			name: "Valid parameter proper response expected",
			params: map[string]string{
				"ID":      strconv.Itoa(toBeClonedID),
				"release": intermediateRelease,
			},
			statusCode: http.StatusOK,
			data: ClonesTemplateData{
				Bug:            toBeCloned,
				Clones:         []*bugzilla.Bug{&intermediateClone, toBeCloned},
				Parent:         toBeCloned,
				PRs:            nil,
				CloneTargets:   []string{"4.8.z", "4.7.z"},
				NewCloneIDs:    []string{strconv.Itoa(toBeClonedID + 1)},
				DependenceTree: &dependenceNode{toBeClonedID, originalTargetRelease, []*dependenceNode{{intermediateClone.ID, intermediateRelease, nil}}},
			},
			tmplt:     clonesTemplate,
			pageTitle: "Clones",
			existingBugs: map[int]bugzilla.Bug{
				toBeClonedID: *toBeCloned,
			},
			allTargetVersions: []string{"4.7.z", "4.8.z", "4.9.z", "4.10.0"},
		},
		{
			name: "Bad params- Non-existent bug ID ",
			params: map[string]string{
				"ID":      "1000",
				"release": "",
			},
			statusCode:        http.StatusNotFound,
			data:              "unable to fetch bug details- Bug#1000",
			tmplt:             errorTemplate,
			pageTitle:         "Not Found",
			allTargetVersions: []string{"4.2.z", "4.3.z", "4.4.z", "4.5.z", "4.6.0"},
		},
		{
			name: "Multiple clones created",
			params: map[string]string{
				"ID":      strconv.Itoa(toBeClonedID),
				"release": clonedRelease,
			},
			statusCode: http.StatusOK,
			data: ClonesTemplateData{
				Bug:            toBeCloned,
				Clones:         []*bugzilla.Bug{&expectedClone, &intermediateClone, toBeCloned},
				Parent:         toBeCloned,
				PRs:            nil,
				CloneTargets:   []string{"4.7.z"},
				NewCloneIDs:    []string{strconv.Itoa(toBeClonedID + 1), strconv.Itoa(toBeClonedID + 2)},
				DependenceTree: &dependenceNode{toBeClonedID, originalTargetRelease, []*dependenceNode{{intermediateClone.ID, intermediateRelease, []*dependenceNode{{expectedClone.ID, clonedRelease, nil}}}}},
			},
			tmplt:     clonesTemplate,
			pageTitle: "Clones",
			existingBugs: map[int]bugzilla.Bug{
				toBeClonedID: *toBeCloned,
			},
			allTargetVersions: []string{"4.7.z", "4.8.z", "4.9.z", "4.10.0"},
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
			fake.Bugs = tc.existingBugs
			handler := CreateCloneHandler(fake, tc.allTargetVersions, fakebzbpMetrics)
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
