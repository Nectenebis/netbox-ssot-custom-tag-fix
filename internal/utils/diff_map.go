package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/bl4ko/netbox-ssot/internal/constants"
	"github.com/bl4ko/netbox-ssot/internal/netbox/objects"
)

// Helper function to determine if a given reflect.Value contains an embedded objects.Choice.
// We assume that Choice attribute is always the first attribute of an object.
func isChoiceEmbedded(v reflect.Value) bool {
	vType := v.Type()
	return vType.Field(0).Type == reflect.TypeOf(objects.Choice{})
	// for i := 0; i < v.NumField(); i++ {
	// 	if vType.Field(i).Type == reflect.TypeOf(objects.Choice{}) {
	// 		return true
	// 	}
	// }
	// return false
}

func choiceValue(v reflect.Value) interface{} {
	return v.Field(0).FieldByName("Value").Interface()
}

// Struct used for patching objects, when attributes are structs, or slices
// we only need object with an ID of original object for patching.
type IDObject struct {
	ID int `json:"id"`
}

// hasPriorityOver returns true if newObj has priority over existingObj, false otherwise.
// newObject will always have priority over exisitngObject, unless following cases:
//  1. When sourcePriority[newObj] > sourcePriority[existingObj]
//  2. When the new IP address is an arp entry (custom field ArpEntry),
//     and the exisiting is not. If both we follow case 1. .
func hasPriorityOver(newObj, existingObj reflect.Value, source2priority map[string]int) bool {
	// Retrieve the SourceName field from CustomFields for both objects
	newObjCustomFields := newObj.FieldByName("CustomFields")
	existingObjCustomFields := existingObj.FieldByName("CustomFields")

	// Check if fields are valid and present in the sourcePriority map
	if newObjCustomFields.IsValid() && existingObjCustomFields.IsValid() {
		if newCustomFields, ok := newObjCustomFields.Interface().(map[string]interface{}); ok {
			if existingCustomFields, ok := existingObjCustomFields.Interface().(map[string]interface{}); ok {
				// 2. case
				if newCustomFields[constants.CustomFieldArpEntryName] != existingCustomFields[constants.CustomFieldArpEntryName] {
					if newCustomFields[constants.CustomFieldArpEntryName] != nil {
						return !newCustomFields[constants.CustomFieldArpEntryName].(bool) //nolint:forcetypeassert
					} else if existingCustomFields[constants.CustomFieldArpEntryName] == true {
						return true
					}
				}

				// 1. case
				if newCustomFields[constants.CustomFieldSourceName] != nil &&
					existingCustomFields[constants.CustomFieldSourceName] != nil {
					newPriority := int(^uint(0) >> 1) // max int
					if priority, newOk := source2priority[newCustomFields[constants.CustomFieldSourceName].(string)]; newOk {
						newPriority = priority
					}
					existingPriority := int(^uint(0) >> 1)
					//nolint:lll
					if priority, existingOk := source2priority[existingCustomFields[constants.CustomFieldSourceName].(string)]; existingOk {
						existingPriority = priority
					}
					// In case newPriority is lower or equal than existingPriority
					// newObj has precedence over exsitingObj
					return newPriority <= existingPriority
				}
			}
		}
	}

	return true
}

// JSONDiffMapExceptID compares two objects and returns a map of fields
// (represented by their JSON tag names) that are different with their
// values from newObj.
// If resetFields is set to true, the function will also include fields
// that are empty in newObj but might have a value in existingObj.
// Also we check for priority, if newObject has priority over existingObject
// we use the fields from newObject, otherwise we use the fields from exisingObject.
func JSONDiffMapExceptID(
	newObj, existingObj interface{},
	resetFields bool,
	source2priority map[string]int,
	customFieldTypes ...map[string]string,
) (map[string]interface{}, error) {
	diff := make(map[string]interface{})

	newObject := reflect.ValueOf(newObj)
	existingObject := reflect.ValueOf(existingObj)

	// Ensure that both objects are of the same kind (e.g. struct)
	if newObject.Kind() != existingObject.Kind() {
		return nil, fmt.Errorf("arguments are not of the same type")
	}

	// Check if the values are pointers and get the element they point to
	if newObject.Kind() == reflect.Pointer {
		newObject = newObject.Elem()
		existingObject = existingObject.Elem()
	}

	// Ensure that we are dealing with structs
	if newObject.Kind() != reflect.Struct {
		return nil, fmt.Errorf("arguments are not structs")
	}

	// Check for priority
	hasPriority := hasPriorityOver(newObject, existingObject, source2priority)

	for i := 0; i < newObject.NumField(); i++ {
		fieldName := newObject.Type().Field(i).Name
		jsonTag := newObject.Type().Field(i).Tag.Get("json")

		if fieldName == "ID" {
			continue
		}

		// Custom logic for all objects that inherit from NetboxObject
		if fieldName == "NetboxObject" {
			netboxObjectDiffMap, err := JSONDiffMapExceptID(
				newObject.Field(i).Interface(),
				existingObject.Field(i).Interface(),
				resetFields,
				source2priority,
				customFieldTypes...,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"error processing JsonDiffMapExceptID when processing NetboxObject %s",
					err,
				)
			}
			for k, v := range netboxObjectDiffMap {
				diff[k] = v
			}
			continue
		}

		// Get json tag, so we can get the json field name
		if jsonTag == "" || jsonTag == "-" {
			jsonTag = fieldName
		} else {
			jsonTag = strings.Split(jsonTag, ",")[0]
		}

		// Ensure that both fields are of the same kind
		if newObject.Field(i).Kind() != existingObject.Field(i).Kind() {
			return nil, fmt.Errorf("field %s is not of the same type in both objects", jsonTag)
		}

		// Check if elements are pointers, in that case get the elements they are pointing to
		newObjectField := newObject.Field(i)
		existingObjectField := existingObject.Field(i)
		if newObjectField.Kind() == reflect.Pointer {
			newObjectField = newObjectField.Elem()
			existingObjectField = existingObjectField.Elem()
		}

		// If reset is set to false and the newObjectField is empty,
		// we don't do anything (like omitempty), see tests
		if !resetFields && (!newObjectField.IsValid() || newObjectField.IsZero()) {
			continue
		}

		switch newObjectField.Kind() {
		// Reset the field (when it is set to nil),
		// this only happens if flag resetFields is set to true.
		case reflect.Invalid:
			if existingObjectField.IsValid() {
				diff[jsonTag] = nil
			}

		case reflect.Slice:
			err := addSliceDiff(newObjectField, existingObjectField, jsonTag, hasPriority, diff)
			if err != nil {
				return nil, fmt.Errorf(
					"error processing JsonDiffMapExceptID when processing slice %s",
					err,
				)
			}

		case reflect.Struct:
			err := addStructDiff(newObjectField, existingObjectField, jsonTag, hasPriority, diff)
			if err != nil {
				return nil, fmt.Errorf(
					"error processing JsonDiffMapExceptID when processing struct %s",
					err,
				)
			}

		case reflect.Map:
			err := addMapDiff(newObjectField, existingObjectField, jsonTag, hasPriority, diff, customFieldTypes...)
			if err != nil {
				return nil, fmt.Errorf(
					"error processing JsonDiffMapExceptID when processing map %s",
					err,
				)
			}

		default:
			addPrimaryDiff(newObjectField, existingObjectField, jsonTag, hasPriority, diff)
		}
	}

	return diff, nil
}

// mergeTagSlices merges two slices of *objects.Tag.
// It keeps all tags from existingSlice that are NOT managed by netbox-ssot
// (i.e. tags whose name does not start with "Source:"),
// and replaces/adds all tags from newSlice.
// Returns the merged slice as []IDObject, whether it changed, and an error.
func mergeTagSlices(
	newSlice reflect.Value,
	existingSlice reflect.Value,
) ([]IDObject, bool, error) {
	// Collect new tags into a map by ID for deduplication
	newTagsByID := make(map[int]*objects.Tag)
	for i := 0; i < newSlice.Len(); i++ {
		elem := newSlice.Index(i)
		if elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		}
		tag, ok := newSlice.Index(i).Interface().(*objects.Tag)
		if !ok {
			// Try non-pointer
			tagVal, ok2 := elem.Interface().(objects.Tag)
			if !ok2 {
				return nil, false, fmt.Errorf("tag slice contains non-Tag element")
			}
			newTagsByID[tagVal.ID] = &tagVal
		} else {
			newTagsByID[tag.ID] = tag
		}
	}

	// Collect IDs of new tags for quick lookup
	newTagIDs := make(map[int]bool, len(newTagsByID))
	for id := range newTagsByID {
		newTagIDs[id] = true
	}

	// Build merged list: start with new tags
	mergedIDs := make(map[int]bool)
	result := make([]IDObject, 0)
	for id := range newTagsByID {
		mergedIDs[id] = true
		result = append(result, IDObject{ID: id})
	}

	// Add existing tags that are NOT managed by netbox-ssot source tagging
	// A tag is considered "managed by source" if its name starts with "Source: "
	if existingSlice.IsValid() {
		for i := 0; i < existingSlice.Len(); i++ {
			elem := existingSlice.Index(i)
			if elem.Kind() == reflect.Pointer {
				elem = elem.Elem()
			}
			var tag objects.Tag
			switch v := elem.Interface().(type) {
			case *objects.Tag:
				tag = *v
			case objects.Tag:
				tag = v
			default:
				return nil, false, fmt.Errorf("existing tag slice contains non-Tag element")
			}
			isManagedBySource := strings.HasPrefix(tag.Name, "Source: ") ||
				tag.Name == constants.SsotTagName ||
				tag.Name == constants.OrphanTagName ||
				tag.Name == constants.IgnoreDeviceTypeTagName
			if !isManagedBySource && !mergedIDs[tag.ID] {
				mergedIDs[tag.ID] = true
				result = append(result, IDObject{ID: tag.ID})
			}
		}
	}

	// Sort result by ID for deterministic output
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})

	existingIDs := make(map[int]bool)
	if existingSlice.IsValid() {
		for i := 0; i < existingSlice.Len(); i++ {
			elem := existingSlice.Index(i)
			if elem.Kind() == reflect.Pointer {
				elem = elem.Elem()
			}
			idField := elem.FieldByName("ID")
			if idField.IsValid() {
				existingIDs[int(idField.Int())] = true
			}
		}
	}
	changed := !reflect.DeepEqual(mergedIDs, existingIDs)

	return result, changed, nil
}

// Function that takes two objects (of type slice) and returns a map
// that can be easily used with json.Marshal
// To achieve this the map is of the following format:
// map[jsonTag] = [id1, id2, id3] // If the slice contains objects with ID field
// map[jsonTag] = [value1, value2, value3] // If the slice contains strings.
//
// This function only works for slices with comparable elements (e.g. ints, strings) and
// slices that contain objects that are structs but have ID attribute.
func addSliceDiff(
	newSlice reflect.Value,
	existingSlice reflect.Value,
	jsonTag string,
	hasPriority bool,
	diffMap map[string]interface{},
) error {
	if !hasPriority {
		return nil
	}

	if !newSlice.IsValid() || newSlice.Len() == 0 {
		if existingSlice.IsValid() && existingSlice.Len() > 0 {
			diffMap[jsonTag] = []interface{}{}
		}
		return nil
	}

	// Merge tags
	if jsonTag == "tags" {
		mergedTags, changed, err := mergeTagSlices(newSlice, existingSlice)
		if err != nil {
			return fmt.Errorf("merge tag slices: %s", err)
		}
		if changed {
			diffMap[jsonTag] = mergedTags
		}
		return nil
	}

	newSlice, err := convertSliceToComparableSlice(newSlice)
	if err != nil {
		return fmt.Errorf("error converting slice to comparable slice: %s", err)
	}
	if !existingSlice.IsValid() || existingSlice.Len() == 0 {
		diffMap[jsonTag] = newSlice.Interface()
		return nil
	}
	existingSlice, err = convertSliceToComparableSlice(existingSlice)
	if err != nil {
		return fmt.Errorf("error converting slice to comparable slice: %s", err)
	}
	newSet := sliceToSet(newSlice)
	existingSet := sliceToSet(existingSlice)
	if !reflect.DeepEqual(newSet, existingSet) {
		diffMap[jsonTag] = newSlice.Interface()
	}
	return nil
}

// Converts slice of structs with IDs to slice containing only ids. If slice
// contains comparable elements don't do anything.
func convertSliceToComparableSlice(slice reflect.Value) (reflect.Value, error) {
	// We determine the types of elements of the slice by checking the first element.
	firstElement := slice.Index(0)
	if firstElement.Kind() == reflect.Pointer {
		firstElement = firstElement.Elem()
	}
	if firstElement.Kind() == reflect.Struct {
		if !firstElement.FieldByName("ID").IsValid() {
			return reflect.ValueOf(
					nil,
				), fmt.Errorf(
					"slice contains struct that don't contain id field",
				)
		}
		idSlice := make([]int, 0)
		for i := 0; i < slice.Len(); i++ {
			element := slice.Index(i)
			if element.Kind() == reflect.Pointer {
				element = element.Elem()
			}
			idField := element.FieldByName("ID").Interface()
			switch value := idField.(type) {
			case int:
				idSlice = append(idSlice, value)
			default:
				return reflect.ValueOf(nil), fmt.Errorf("id is not int")
			}
		}
		return reflect.ValueOf(idSlice), nil
	}

	return slice, nil
}

// Converts slice to a set.
func sliceToSet(slice reflect.Value) map[interface{}]bool {
	set := make(map[interface{}]bool)
	for i := 0; i < slice.Len(); i++ {
		element := slice.Index(i)
		if element.Kind() == reflect.Pointer {
			element = element.Elem()
		}
		set[element.Interface()] = true
	}
	return set
}

// Returns json form for patching the difference e.g. { "ID": 1 }.
func addStructDiff(
	newObj reflect.Value,
	existingObj reflect.Value,
	jsonTag string,
	hasPriority bool,
	diffMap map[string]interface{},
) error {
	// If first struct is nil, that means that we reset the attribute to nil
	if !newObj.IsValid() {
		diffMap[jsonTag] = nil
		return nil
	}

	// We check if struct is a objects.Choice (special netbox struct)
	if isChoiceEmbedded(newObj) {
		if !existingObj.IsValid() || newObj.Interface() != existingObj.Interface() {
			diffMap[jsonTag] = choiceValue(newObj)
		}
		return nil
	}

	// We use ids for comparison between structs, because for patching objects, all we need is id of attribute
	idField := newObj.FieldByName("ID")

	// If objects don't have ID field, compare them by their values
	if !idField.IsValid() {
		if !existingObj.IsValid() {
			diffMap[jsonTag] = newObj.Interface()
		} else if newObj.Interface() != existingObj.Interface() {
			if hasPriority {
				diffMap[jsonTag] = newObj.Interface()
			}
		}
	} else {
		if !existingObj.IsValid() {
			idValue, ok := idField.Interface().(int)
			if !ok {
				return fmt.Errorf("id field is not an int")
			}
			diffMap[jsonTag] = IDObject{ID: idValue}
		} else if newObj.FieldByName("ID").Interface() != existingObj.FieldByName("ID").Interface() {
			// Objects have ID field, compare their ids
			idValue, ok := idField.Interface().(int)
			if !ok {
				return fmt.Errorf("id field is not an int")
			}
			diffMap[jsonTag] = IDObject{ID: idValue}
		}
	}
	return nil
}

// addMapDiff emits a partial custom_fields update: NetBox merges these keys with
// existing values. Replaying omitted fields would send API read representations
// (such as selection objects) back to an API expecting write representations.
func addMapDiff(
	newMap reflect.Value,
	existingMap reflect.Value,
	jsonTag string,
	hasPriority bool,
	diffMap map[string]interface{},
	customFieldTypes ...map[string]string,
) error {
	if !newMap.IsValid() {
		return nil
	}
	mapsDiff := make(map[string]interface{})
	for _, key := range newMap.MapKeys() {
		name, ok := key.Interface().(string)
		if !ok {
			return fmt.Errorf("map keys have to be strings. Not implemented for anything else yet")
		}
		value := newMap.MapIndex(key).Interface()
		var existing reflect.Value
		if existingMap.IsValid() {
			existing = existingMap.MapIndex(key)
		}
		fieldType := ""
		if jsonTag == "custom_fields" && len(customFieldTypes) > 0 {
			fieldType = customFieldTypes[0][name]
			value = normalizeCustomFieldValue(value, fieldType)
		}
		if !existing.IsValid() {
			// Preserve the existing priority rule: sources may add absent keys.
			mapsDiff[name] = value
		} else if hasPriority {
			oldValue := existing.Interface()
			if jsonTag == "custom_fields" {
				oldValue = normalizeCustomFieldValue(oldValue, fieldType)
			}
			equal := reflect.DeepEqual(value, oldValue)
			if jsonTag == "custom_fields" {
				equal = customFieldValuesEqual(value, oldValue)
			}
			if !equal {
				mapsDiff[name] = value
			}
		}
	}
	if len(mapsDiff) > 0 {
		// Other map fields retain their existing replacement semantics.
		if jsonTag != "custom_fields" && existingMap.IsValid() {
			for _, key := range existingMap.MapKeys() {
				if name, ok := key.Interface().(string); ok && !newMap.MapIndex(key).IsValid() {
					if value := existingMap.MapIndex(key).Interface(); value != nil {
						mapsDiff[name] = value
					}
				}
			}
		}
		diffMap[jsonTag] = mapsDiff
	}
	return nil
}

// Normalize only fields whose NetBox type is known. In particular, JSON fields
// may legitimately contain value, label, or id keys and must remain intact.
func normalizeCustomFieldValue(value interface{}, fieldType string) interface{} {
	switch fieldType {
	case "select":
		if choice, ok := value.(map[string]interface{}); ok {
			if raw, exists := choice["value"]; exists {
				return raw
			}
		}
	case "object":
		if object, ok := value.(map[string]interface{}); ok {
			if id, exists := object["id"]; exists {
				return id
			}
		}
	case "multiselect", "multiobject":
		items := reflect.ValueOf(value)
		if items.IsValid() && items.Kind() == reflect.Slice && !items.IsNil() {
			itemType := "select"
			if fieldType == "multiobject" {
				itemType = "object"
			}
			normalized := make([]interface{}, items.Len())
			for i := range normalized {
				normalized[i] = normalizeCustomFieldValue(items.Index(i).Interface(), itemType)
			}
			// Both field types represent unordered selections.
			sort.SliceStable(normalized, func(i, j int) bool {
				left, _ := json.Marshal(normalized[i])
				right, _ := json.Marshal(normalized[j])
				return bytes.Compare(left, right) < 0
			})
			return normalized
		}
	}
	return value
}

func customFieldValuesEqual(left, right interface{}) bool {
	if reflect.DeepEqual(left, right) {
		return true
	}
	// API numbers decode as float64, while source values may be Go integers.
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func addPrimaryDiff(
	newField reflect.Value,
	existingField reflect.Value,
	jsonTag string,
	hasPriority bool,
	diffMap map[string]interface{},
) {
	switch {
	case newField.IsZero():
		if !existingField.IsZero() {
			diffMap[jsonTag] = reflect.Zero(newField.Type()).Interface()
		}
	case existingField.IsZero():
		diffMap[jsonTag] = newField.Interface()
	case newField.Interface() != existingField.Interface():
		if hasPriority {
			diffMap[jsonTag] = newField.Interface()
		}
	}
}

func ExtractFieldsFromDiffMap(
	diffMap map[string]interface{},
	field []string,
) map[string]interface{} {
	extractedFields := make(map[string]interface{})
	if len(diffMap) == 0 {
		return extractedFields
	}
	for _, f := range field {
		if value, ok := diffMap[f]; ok {
			extractedFields[f] = value
		}
	}
	return extractedFields
}
