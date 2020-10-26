package main

import (
	"fmt"
	"path/filepath"
	"reflect"

	"github.com/sirupsen/logrus"
	"k8s.io/test-infra/pkg/genyaml"

	"github.com/openshift/ci-tools/pkg/api"
)

func main() {
	files, err := filepath.Glob("./pkg/api/*.go")
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct glob for api files")
	}
	res, err := genyaml.NewCommentMap(files...).GenYaml(populateSubStructures(&api.ReleaseBuildConfiguration{}))
	if err != nil {
		logrus.WithError(err).Fatal("GenYaml failed")
	}
	fmt.Println(res)
}

func populateSubStructures(in interface{}) interface{} {

	typeOf := reflect.TypeOf(in)
	if typeOf.Kind() != reflect.Ptr {
		panic(fmt.Sprintf("got nonpointer type %T", in))
	}
	if typeOf.Elem().Kind() != reflect.Struct {
		return in
	}
	valueOf := reflect.ValueOf(in)
	for i := 0; i < typeOf.Elem().NumField(); i++ {
		switch k := typeOf.Elem().Field(i).Type.Kind(); k {
		case reflect.String:
			// We must populate strings, because genyaml uses a custom json lib
			// that omits structs that have empty values only and we have some
			// structs that only have string fields
			valueOf.Elem().Field(i).SetString(" ")
		case reflect.Ptr:
			ptr := createNonNilPtr(valueOf.Elem().Field(i).Type())
			// Populate our ptr
			if ptr.Elem().Kind() == reflect.Struct {
				populateSubStructures(ptr.Interface())
			}
			// Set it on the parent struct
			valueOf.Elem().Field(i).Set(ptr)
		case reflect.Slice:
			// Create a one element slice
			slice := reflect.MakeSlice(typeOf.Elem().Field(i).Type, 1, 1)
			// Get a pointer to the value
			var sliceElementPtr interface{}
			if slice.Index(0).Type().Kind() == reflect.Ptr {
				// Slice of pointers, make it a non-nil pointer, then pass on its address
				slice.Index(0).Set(createNonNilPtr(slice.Index(0).Type()))
				sliceElementPtr = slice.Index(0).Interface()
			} else {
				// Slice of literals
				sliceElementPtr = slice.Index(0).Addr().Interface()
			}
			populateSubStructures(sliceElementPtr)
			// Set it on the parent struct
			valueOf.Elem().Field(i).Set(slice)
		case reflect.Map:
			keyType := typeOf.Elem().Field(i).Type.Key()
			valueType := typeOf.Elem().Field(i).Type.Elem()

			key := reflect.New(keyType).Elem()
			value := reflect.New(valueType).Elem()

			var keyPtr, valPtr interface{}
			if key.Kind() == reflect.Ptr {
				keyPtr = key.Interface()
			} else {
				keyPtr = key.Addr().Interface()
			}
			if value.Kind() == reflect.Ptr {
				valPtr = value.Interface()
			} else {
				valPtr = value.Addr().Interface()
			}
			populateSubStructures(keyPtr)
			populateSubStructures(valPtr)

			mapType := reflect.MapOf(typeOf.Elem().Field(i).Type.Key(), typeOf.Elem().Field(i).Type.Elem())
			concreteMap := reflect.MakeMapWithSize(mapType, 0)
			concreteMap.SetMapIndex(key, value)

			valueOf.Elem().Field(i).Set(concreteMap)
		}

	}
	return in
}

func createNonNilPtr(in reflect.Type) reflect.Value {
	// construct a new **type and call Elem() to get the *type
	ptr := reflect.New(in).Elem()
	// Give it a value
	ptr.Set(reflect.New(ptr.Type().Elem()))

	return ptr
}
