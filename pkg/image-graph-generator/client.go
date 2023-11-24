package imagegraphgenerator

import (
	"context"
	"encoding/json"
	"fmt"
)

type Client interface {
	Mutate(context.Context, interface{}, map[string]interface{}) error
	Query(context.Context, interface{}, map[string]interface{}) error
}

type fakeClient struct {
	images     map[string]Image
	uidCounter int
}

func NewFakeClient() *fakeClient {
	return &fakeClient{
		images: make(map[string]Image),
	}
}

func (fc *fakeClient) UID() int {
	fc.uidCounter++
	return fc.uidCounter
}

func (fc *fakeClient) Mutate(ctx context.Context, iface interface{}, variables map[string]interface{}) error {
	if iface, ok := variables["input"]; ok {
		switch v := iface.(type) {
		case []AddImageInput:
			for _, input := range v {
				data, err := json.Marshal(input)
				if err != nil {
					return err
				}
				var image Image
				if err := json.Unmarshal(data, &image); err != nil {
					return err
				}
				uid := fmt.Sprintf("%d", fc.UID())
				image.ID = uid
				fc.images[uid] = image
			}
		case UpdateImageInput:
			f := v["filter"]
			p := v["set"]

			var id string
			if filter, ok := f.(ImageFilter); ok {
				id = fmt.Sprintf("%s", filter["id"])

				if patch, ok := p.(ImagePatch); ok {
					img := fc.images[id]
					img.ID = id

					if name, ok := patch["name"]; ok {
						img.Name = fmt.Sprintf("%s", name)
					}
					if namespace, ok := patch["namespace"]; ok {
						img.Namespace = fmt.Sprintf("%s", namespace)
					}
					if imageStreamRef, ok := patch["imageStreamRef"]; ok {
						img.ImageStreamRef = fmt.Sprintf("%s", imageStreamRef)
					}
					if fromRoot, ok := patch["fromRoot"]; ok {
						if value, ok := fromRoot.(bool); ok {
							img.FromRoot = value
						}
					}
					if branches, ok := patch["branches"]; ok {
						if value, ok := branches.(map[string]interface{}); ok {
							img.Branches = value
						}
					}

					fc.images[id] = img
				}
			}
		}
	}
	return nil
}

func (fc *fakeClient) Query(ctx context.Context, iface interface{}, variables map[string]interface{}) error {
	return nil
}
