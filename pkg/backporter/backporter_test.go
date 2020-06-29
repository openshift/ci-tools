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

func unwrapper(h HandlerFuncWithErrorReturn) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = h(w, r)
	})
}
func TestGetLandingHandler(t *testing.T) {
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	handler := unwrapper(GetLandingHandler())
	handler.ServeHTTP(rr, req)
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Error fetching landing page for bugzilla backporter tool!")
	}
	if resp := rr.Body.String(); resp != landingPage {
		t.Errorf("Might not have changed the landingPage after modifying it in the backporter tool - Response differs from expected by: %s", diff.StringDiff(resp, landingPage))
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
			handler := unwrapper(GetBugHandler(fake))
			handler.ServeHTTP(rr, req)
			if status := rr.Code; status != tc.statusCode {
				t.Errorf("testcase '%v' failed: getbug returned wrong status code - got %v, want %v", tc.name, status, tc.statusCode)
			}
		})
	}

}

type ResCheck struct {
	statusCode int
	data       interface{}
	tmplt      *template.Template
}

func TestGetClonesHandler(t *testing.T) {
	fake := &bugzilla.Fake{}
	fake.Bugs = map[int]bugzilla.Bug{}
	fake.BugComments = map[int][]bugzilla.Comment{}

	bug1Create := &bugzilla.BugCreate{
		AssignedTo: "UnitTest",
		Summary:    "Sample bug to test implementation of clones handler",
	}
	bug1ID, err := fake.CreateBug(bug1Create)
	if err != nil {
		t.Errorf("Error creating bug: %v", err)
	}
	bug1, err := fake.GetBug(bug1ID)
	if err != nil {
		t.Errorf("Error retreiving bug: %v", err)
	}
	cloneID, err := fake.CloneBug(bug1)
	if err != nil {
		t.Errorf("Error while cloning bug: %v", err)
	}
	clone, err := fake.GetBug(cloneID)
	if err != nil {
		t.Errorf("Error retreiving clone: %v", err)
	}
	testCases := []struct {
		name    string
		params  map[string]int
		results ResCheck
	}{
		{
			"valid_parameters",
			map[string]int{
				"ID": cloneID,
			},
			ResCheck{
				http.StatusOK,
				ClonesTemplateData{
					clone,
					[]*bugzilla.Bug{
						bug1,
					},
					bug1,
					nil,
					allTargetVersions.List(),
					0,
				},
				clonesTemplate,
			},
		},
		{
			"bad_params",
			map[string]int{
				"ID": 1000,
			},
			ResCheck{
				http.StatusNotFound,
				"Bug#1000 not found",
				errorTemplate,
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", "/clones", nil)
			if err != nil {
				t.Fatal(err)
			}
			q := req.URL.Query()
			for k, v := range tc.params {
				q.Add(k, strconv.Itoa(v))
			}
			req.URL.RawQuery = q.Encode()
			rr := httptest.NewRecorder()
			handler := unwrapper(ClonesHandler(fake, allTargetVersions))
			handler.ServeHTTP(rr, req)
			if status := rr.Code; status != tc.results.statusCode {
				t.Errorf("testcase '%v' failed: getbug returned wrong status code - got %v, want %v", tc, status, tc.results.statusCode)
			}
			var buf bytes.Buffer
			if err := tc.results.tmplt.Execute(&buf, tc.results.data); err != nil {
				t.Errorf("Unable to render template: %v", err)
			}
			subPage := buf.String()
			var expResponse string
			if tc.results.statusCode == http.StatusOK {
				expResponse = fmt.Sprintf(htmlPageStart, "Clones") + subPage + htmlPageEnd
			} else {
				expResponse = fmt.Sprintf(htmlPageStart, "Not Found") + subPage + htmlPageEnd
			}
			if resp := rr.Body.String(); resp != expResponse {
				t.Errorf("Response differs from expected by: %s", diff.StringDiff(resp, expResponse))
			}
		})
	}

}

func TestCreateCloneHandler(t *testing.T) {
	fake := &bugzilla.Fake{}
	fake.Bugs = map[int]bugzilla.Bug{}
	fake.BugComments = map[int][]bugzilla.Comment{}

	bug1Create := &bugzilla.BugCreate{
		AssignedTo: "UnitTest",
		Summary:    "Sample bug to test implementation of clones handler",
	}
	bug1ID, err := fake.CreateBug(bug1Create)
	if err != nil {
		t.Errorf("Error creating bug: %v", err)
	}
	clonedRelease := "4.4.z"
	prunedReleaseSet := sets.NewString(allTargetVersions.List()...)
	prunedReleaseSet.Delete(clonedRelease)

	bug1, err := fake.GetBug(bug1ID)
	if err != nil {
		t.Errorf("Error getting bug details from Fake.")
	}
	expectedCloneID := bug1ID + 1
	testcases := []struct {
		name    string
		params  map[string]string
		results ResCheck
	}{
		{
			"valid_parameters",
			map[string]string{
				"ID":      strconv.Itoa(bug1ID),
				"release": clonedRelease,
			},
			ResCheck{
				http.StatusOK,
				ClonesTemplateData{
					bug1,
					[]*bugzilla.Bug{},
					bug1,
					nil,
					prunedReleaseSet.List(),
					expectedCloneID,
				},
				clonesTemplate,
			},
		},
		{
			"bad_params",
			map[string]string{
				"ID":      "1000",
				"release": "",
			},
			ResCheck{
				http.StatusNotFound,
				"Unable to fetch bug details- Bug#1000 : bug not registered in the fake",
				errorTemplate,
			},
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
			handler := unwrapper(CreateCloneHandler(fake, allTargetVersions))
			handler.ServeHTTP(rr, req)
			if status := rr.Code; status != tc.results.statusCode {
				t.Errorf("testcase '%v' failed: clonebug returned wrong status code - got %v, want %v", tc, status, tc.results.statusCode)
			}

			var buf bytes.Buffer
			var pageStart string
			if tc.results.statusCode == http.StatusOK {
				pageStart = fmt.Sprintf(htmlPageStart, "Clones")
				newClone, err := fake.GetBug(expectedCloneID)
				if err != nil {
					t.Fatalf("Error while fetching clone details from mocked endpoint")
				}
				newClone.TargetRelease = []string{
					tc.params["release"],
				}
				data, ok := tc.results.data.(ClonesTemplateData)
				if ok {
					data.Clones = []*bugzilla.Bug{
						newClone,
					}
				}
				tc.results.data = data

			} else {
				pageStart = fmt.Sprintf(htmlPageStart, "Not Found")
			}
			if err := tc.results.tmplt.Execute(&buf, tc.results.data); err != nil {
				t.Errorf("Unable to render template: %v", err)
			}
			subPage := buf.String()
			expResponse := pageStart + subPage + htmlPageEnd

			if resp := rr.Body.String(); resp != expResponse {
				t.Errorf("Response differs from expected by: %s", diff.StringDiff(resp, expResponse))
			}
		})
	}
}
