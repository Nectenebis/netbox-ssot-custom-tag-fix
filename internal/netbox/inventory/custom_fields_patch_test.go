package inventory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/bl4ko/netbox-ssot/internal/constants"
	"github.com/bl4ko/netbox-ssot/internal/netbox/objects"
	"github.com/bl4ko/netbox-ssot/internal/netbox/service"
)

// Exercise the real AddVM -> diff -> HTTP PATCH -> inventory response path.
// The server applies NetBox's partial custom_fields merge and rejects replayed
// user-owned fields, including read-format choices and JSON with an id key.
func TestAddVMPartialCustomFieldsPatch(t *testing.T) {
	t.Parallel()
	for _, managedChoice := range []bool{false, true} {
		name := "user-owned selection"
		if managedChoice {
			name = "source-managed selection"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tag := &objects.Tag{ID: 1, Name: constants.SsotTagName}
			stored := &objects.VM{
				NetboxObject: objects.NetboxObject{
					ID: 3219, Tags: []*objects.Tag{tag},
					CustomFields: map[string]interface{}{
						"source": "mvcenter", "source_id": "old-id", "orphan_last_seen": nil,
						"fond":        map[string]interface{}{"value": "future", "label": "Будущее"},
						"environment": map[string]interface{}{"value": "test", "label": "Test"},
						"choices":     []interface{}{map[string]interface{}{"value": "a", "label": "A"}},
						"object":      map[string]interface{}{"id": float64(5), "display": "Site"},
						"objects":     []interface{}{map[string]interface{}{"id": float64(6)}},
						"metadata":    map[string]interface{}{"id": float64(7), "value": "literal", "label": "keep"},
					},
				},
				Name: "mOCRsql-test", Cluster: &objects.Cluster{NetboxObject: objects.NetboxObject{ID: 62}},
			}
			originalFields := make(map[string]interface{}, len(stored.CustomFields))
			for key, value := range stored.CustomFields {
				originalFields[key] = value
			}
			var patches atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPatch || r.URL.Path != "/api/virtualization/virtual-machines/3219/" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected request", http.StatusBadRequest)
					return
				}
				var body struct {
					CustomFields map[string]interface{} `json:"custom_fields"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					http.Error(w, "invalid JSON", http.StatusBadRequest)
					return
				}
				want := map[string]interface{}{"source_id": "new-id"}
				if managedChoice {
					want["fond"] = "evolution"
				}
				if !reflect.DeepEqual(body.CustomFields, want) {
					t.Errorf("PATCH fields = %#v, want %#v", body.CustomFields, want)
					http.Error(w, "invalid custom fields payload", http.StatusBadRequest)
					return
				}
				for key, value := range body.CustomFields {
					if key == "fond" {
						value = map[string]interface{}{"value": value, "label": "Эволюция"}
					}
					stored.CustomFields[key] = value
				}
				patches.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(stored); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			nbi := &NetboxInventory{
				Logger: mockLogger, SsotTag: tag, OrphanManager: NewOrphanManager(mockLogger),
				NetboxAPI: &service.NetboxClient{
					Logger: mockLogger, HTTPClient: server.Client(), BaseURL: server.URL, Timeout: 5,
				},
				vmsIndexByNameAndClusterID: map[string]map[int]*objects.VM{stored.Name: {62: stored}},
				vmsIndexByID:               map[int]*objects.VM{stored.ID: stored},
				customFieldsIndexByName: map[string]*objects.CustomField{
					"fond": {Name: "fond", Type: objects.CustomFieldType{Choice: objects.Choice{Value: "select"}}},
				},
			}
			ctx := context.WithValue(context.Background(), constants.CtxSourceKey, "mvcenter")
			for range 2 {
				fields := map[string]interface{}{"source_id": "new-id"}
				if managedChoice {
					fields["fond"] = "evolution"
				}
				got, err := nbi.AddVM(ctx, &objects.VM{
					NetboxObject: objects.NetboxObject{CustomFields: fields},
					Name:         stored.Name, Cluster: stored.Cluster,
				})
				if err != nil {
					t.Fatal(err)
				}
				if got.ID != 3219 {
					t.Fatalf("VM ID changed: %d", got.ID)
				}
				for key, value := range originalFields {
					if key == "source_id" || (managedChoice && key == "fond") {
						continue
					}
					if !reflect.DeepEqual(got.CustomFields[key], value) {
						t.Errorf("custom field %s changed: %#v", key, got.CustomFields[key])
					}
				}
			}
			if patches.Load() != 1 {
				t.Errorf("got %d PATCH requests, want 1", patches.Load())
			}
		})
	}
}
