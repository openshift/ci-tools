package backporter

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"k8s.io/test-infra/prow/bugzilla"
	"k8s.io/utils/diff"
)

var (
	landingPage    = fmt.Sprintf(htmlPageStart, "Home") + htmlPageEnd
	clonesHTMLPage = fmt.Sprintf(htmlPageStart, "Clones") + clonesHTMLSubPage + htmlPageEnd
	errorPage      = fmt.Sprintf(htmlPageStart, "Not Found") + errorSubPage + htmlPageEnd
)

const clonesHTMLSubPage = `
	<h2 id="bugid"> <a href = "#bugid"> 1: Sample bug to test implementation of clones handler </a> | Status:  </h2>
	<p> Target Release: [] </p>
	
		<p> No linked PRs! </p>
	
	
		<p> Cloned From: <a href = "/getclones?ID=0"> Bug 0: Sample bug to test implementation of clones handler</a> | Status: 
	
	<h4 id="clones"> <a href ="#clones"> Clones</a> </h4>
	<table class="table">
		<thead>
			<tr>
				<th title="Targeted version to release fix" class="info">Target Release</th>
				<th title="ID of the cloned bug" class="info">Bug ID</th>
				<th title="Status of the cloned bug" class="info">Status</th>
				<th title="PR associated with this bug" class="info">PR</th>
			</tr>
		</thead>
		<tbody>
		
			
				<tr>
					<td style="vertical-align: middle;">[]</td>
					<td style="vertical-align: middle;"><a href = "/getclones?ID=0">0</a></td>
					<td style="vertical-align: middle;"></td>
					<td style="vertical-align: middle;">
						
					</td>
				</tr>
			
		
		</tbody>
	</table>`

const errorSubPage = `Bug#1000 not found`

func unwrapper(h HandlerFuncWithErrorReturn) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h(w, r)
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
			req, err := http.NewRequest("GET", "/getbug", nil)
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
	htmlPage   string
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
				clonesHTMLPage,
			},
		},
		{
			"bad_params",
			map[string]int{
				"ID": 1000,
			},
			ResCheck{
				http.StatusNotFound,
				errorPage,
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", "/getclones", nil)
			if err != nil {
				t.Fatal(err)
			}
			q := req.URL.Query()
			for k, v := range tc.params {
				q.Add(k, strconv.Itoa(v))
			}
			req.URL.RawQuery = q.Encode()
			rr := httptest.NewRecorder()
			handler := unwrapper(GetClonesHandler(fake))
			handler.ServeHTTP(rr, req)
			if status := rr.Code; status != tc.results.statusCode {
				t.Errorf("testcase '%v' failed: getbug returned wrong status code - got %v, want %v", tc, status, tc.results.statusCode)
			}
			if resp := rr.Body.String(); resp != tc.results.htmlPage {
				t.Errorf("Response differs from expected by: %s", diff.StringDiff(resp, tc.results.htmlPage))
			}
		})
	}

}
