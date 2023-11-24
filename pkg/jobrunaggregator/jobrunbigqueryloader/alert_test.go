package jobrunbigqueryloader

import (
	"testing"
	"time"

	"cloud.google.com/go/bigquery"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
)

func TestPopulateZeros(t *testing.T) {

	twoMonthsAgo := time.Now().Add(-2 * 30 * 24 * time.Hour)
	fourDaysAgo := time.Now().Add(-4 * 24 * time.Hour)

	knownAlerts := []*jobrunaggregatorapi.KnownAlertRow{
		{
			AlertName:      "etcdMembersDown",
			AlertNamespace: "",
			AlertLevel:     "Critical",
			Release:        "4.13",
			FirstObserved:  twoMonthsAgo,
			LastObserved:   fourDaysAgo,
		},
		{
			AlertName:      "TargetDown",
			AlertNamespace: "kube-system",
			AlertLevel:     "Critical",
			Release:        "4.13",
			FirstObserved:  twoMonthsAgo,
			LastObserved:   fourDaysAgo,
		},
		{
			AlertName:      "PodDisruptionBudgetLimit",
			AlertNamespace: "openshift-apiserver",
			AlertLevel:     "Warning",
			Release:        "4.13",
			FirstObserved:  twoMonthsAgo,
			LastObserved:   fourDaysAgo,
		},
	}
	knownAlertsCache := newKnownAlertsCache(knownAlerts)
	tests := []struct {
		name                   string
		observedAlerts         []jobrunaggregatorapi.AlertRow
		expectedAlertsToUpload []jobrunaggregatorapi.AlertRow
	}{
		{
			// No need to inject zeros if we saw everything, but this case would be exceedingly rare:
			name: "observed all known alerts",
			observedAlerts: []jobrunaggregatorapi.AlertRow{
				{
					JobRunName:   "1000",
					Name:         "etcdMembersDown",
					Namespace:    "",
					Level:        "Critical",
					AlertSeconds: 10,
				},
				{
					JobRunName:   "1000",
					Name:         "TargetDown",
					Namespace:    "kube-system",
					Level:        "Critical",
					AlertSeconds: 200,
				},
				{
					JobRunName:   "1000",
					Name:         "PodDisruptionBudgetLimit",
					Namespace:    "openshift-apiserver",
					Level:        "Warning",
					AlertSeconds: 50,
				},
			},
			expectedAlertsToUpload: []jobrunaggregatorapi.AlertRow{
				{
					JobRunName:   "1000",
					Name:         "etcdMembersDown",
					Namespace:    "",
					Level:        "Critical",
					AlertSeconds: 10,
				},
				{
					JobRunName:   "1000",
					Name:         "TargetDown",
					Namespace:    "kube-system",
					Level:        "Critical",
					AlertSeconds: 200,
				},
				{
					JobRunName:   "1000",
					Name:         "PodDisruptionBudgetLimit",
					Namespace:    "openshift-apiserver",
					Level:        "Warning",
					AlertSeconds: 50,
				},
			},
		},
		{
			// Every known alert should get injected as a 0
			name:           "no alerts observed",
			observedAlerts: []jobrunaggregatorapi.AlertRow{},
			expectedAlertsToUpload: []jobrunaggregatorapi.AlertRow{
				{
					JobRunName:   "1000",
					Name:         "etcdMembersDown",
					Namespace:    "",
					Level:        "Critical",
					AlertSeconds: 0,
				},
				{
					JobRunName:   "1000",
					Name:         "TargetDown",
					Namespace:    "kube-system",
					Level:        "Critical",
					AlertSeconds: 0,
				},
				{
					JobRunName:   "1000",
					Name:         "PodDisruptionBudgetLimit",
					Namespace:    "openshift-apiserver",
					Level:        "Warning",
					AlertSeconds: 0,
				},
			},
		},
		{
			name: "previously unknown alert",
			observedAlerts: []jobrunaggregatorapi.AlertRow{
				{
					JobRunName:   "1000",
					Name:         "etcdMembersDown",
					Namespace:    "",
					Level:        "Critical",
					AlertSeconds: 10,
				},
				{
					JobRunName:   "1000",
					Name:         "TargetDown",
					Namespace:    "kube-system",
					Level:        "Critical",
					AlertSeconds: 200,
				},
				{
					JobRunName:   "1000",
					Name:         "PodDisruptionBudgetLimit",
					Namespace:    "openshift-apiserver",
					Level:        "Warning",
					AlertSeconds: 50,
				},
				{
					JobRunName:   "1000",
					Name:         "etcdHighCommitDurations",
					Namespace:    "",
					Level:        "Warning",
					AlertSeconds: 50,
				},
			},
			expectedAlertsToUpload: []jobrunaggregatorapi.AlertRow{
				{
					JobRunName:   "1000",
					Name:         "etcdMembersDown",
					Namespace:    "",
					Level:        "Critical",
					AlertSeconds: 10,
				},
				{
					JobRunName:   "1000",
					Name:         "TargetDown",
					Namespace:    "kube-system",
					Level:        "Critical",
					AlertSeconds: 200,
				},
				{
					JobRunName:   "1000",
					Name:         "PodDisruptionBudgetLimit",
					Namespace:    "openshift-apiserver",
					Level:        "Warning",
					AlertSeconds: 50,
				},
				{
					JobRunName:   "1000",
					Name:         "etcdHighCommitDurations",
					Namespace:    "",
					Level:        "Warning",
					AlertSeconds: 50,
				},
			},
		},
		{
			name: "known alert in new namespace",
			observedAlerts: []jobrunaggregatorapi.AlertRow{
				{
					JobRunName:   "1000",
					Name:         "etcdMembersDown",
					Namespace:    "",
					Level:        "Critical",
					AlertSeconds: 10,
				},
				{
					JobRunName:   "1000",
					Name:         "TargetDown",
					Namespace:    "kube-system",
					Level:        "Critical",
					AlertSeconds: 200,
				},
				{
					JobRunName:   "1000",
					Name:         "PodDisruptionBudgetLimit",
					Namespace:    "openshift-apiserver",
					Level:        "Warning",
					AlertSeconds: 50,
				},
				{
					JobRunName:   "1000",
					Name:         "TargetDown",
					Namespace:    "openshift-kube-apiserver",
					Level:        "Warning",
					AlertSeconds: 50,
				},
			},
			expectedAlertsToUpload: []jobrunaggregatorapi.AlertRow{
				{
					JobRunName:   "1000",
					Name:         "etcdMembersDown",
					Namespace:    "",
					Level:        "Critical",
					AlertSeconds: 10,
				},
				{
					JobRunName:   "1000",
					Name:         "TargetDown",
					Namespace:    "kube-system",
					Level:        "Critical",
					AlertSeconds: 200,
				},
				{
					JobRunName:   "1000",
					Name:         "PodDisruptionBudgetLimit",
					Namespace:    "openshift-apiserver",
					Level:        "Warning",
					AlertSeconds: 50,
				},
				{
					JobRunName:   "1000",
					Name:         "TargetDown",
					Namespace:    "openshift-kube-apiserver",
					Level:        "Warning",
					AlertSeconds: 50,
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			jobRunRow := &jobrunaggregatorapi.JobRunRow{
				Name:               "1000",
				JobName:            "foobar-job",
				Cluster:            "build01",
				ReleaseTag:         "",
				MasterNodesUpdated: bigquery.NullString{StringVal: "", Valid: true},
			}
			// Add some more detail to the observed/expected alerts as the bigquery null
			// types get quite verbose to include in every test:
			for i := range test.observedAlerts {
				test.observedAlerts[i].JobName = bigquery.NullString{
					StringVal: "foobar-job",
					Valid:     true,
				}
				test.observedAlerts[i].Cluster = bigquery.NullString{
					StringVal: "build01",
					Valid:     true,
				}
				test.observedAlerts[i].ReleaseTag = bigquery.NullString{
					StringVal: "",
					Valid:     true,
				}
				test.observedAlerts[i].JobRunStartTime = bigquery.NullTimestamp{
					Timestamp: time.Time{},
					Valid:     true,
				}
				test.observedAlerts[i].JobRunEndTime = bigquery.NullTimestamp{
					Timestamp: time.Time{},
					Valid:     true,
				}
				test.observedAlerts[i].JobRunStatus = bigquery.NullString{
					StringVal: "",
					Valid:     true,
				}
				test.observedAlerts[i].MasterNodesUpdated = bigquery.NullString{
					StringVal: "",
					Valid:     true,
				}
			}
			// Add some more detail to the observed/expected alerts as the bigquery null
			// types get quite verbose to include in every test:
			for i := range test.expectedAlertsToUpload {
				test.expectedAlertsToUpload[i].JobName = bigquery.NullString{
					StringVal: "foobar-job",
					Valid:     true,
				}
				test.expectedAlertsToUpload[i].Cluster = bigquery.NullString{
					StringVal: "build01",
					Valid:     true,
				}
				test.expectedAlertsToUpload[i].ReleaseTag = bigquery.NullString{
					StringVal: "",
					Valid:     true,
				}
				test.expectedAlertsToUpload[i].JobRunStartTime = bigquery.NullTimestamp{
					Timestamp: time.Time{},
					Valid:     true,
				}
				test.expectedAlertsToUpload[i].JobRunEndTime = bigquery.NullTimestamp{
					Timestamp: time.Time{},
					Valid:     true,
				}
				test.expectedAlertsToUpload[i].JobRunStatus = bigquery.NullString{
					StringVal: "",
					Valid:     true,
				}
				test.expectedAlertsToUpload[i].MasterNodesUpdated = bigquery.NullString{
					StringVal: "",
					Valid:     true,
				}
			}
			results := populateZeros(jobRunRow, knownAlertsCache, test.observedAlerts,
				"4.13", logrus.WithField("test", test.name))
			assert.Equal(t, test.expectedAlertsToUpload, results)
		})
	}
}
