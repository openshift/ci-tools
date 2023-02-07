package imagegraphgenerator

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
)

type ImageRef struct {
	ID             string      `graphql:"id"`
	Name           string      `graphql:"name"`
	ImageStreamRef string      `graphql:"imageStreamRef"`
	Namespace      string      `graphql:"namespace"`
	FromRoot       bool        `graphql:"fromRoot"`
	Source         string      `graphql:"source"`
	Branches       []BranchRef `graphql:"branches"`
	Parent         *ImageRef   `graphql:"parent"`
	Children       []ImageRef  `graphql:"children"`
}

func (o *Operator) UpdateImage(image api.ProjectDirectoryImageBuildStepConfiguration, c *api.ReleaseBuildConfiguration, branchID string) error {
	imageName := fmt.Sprintf("%s/%s:%s", c.PromotionConfiguration.Namespace, c.PromotionConfiguration.Name, string(image.To))
	if c.PromotionConfiguration.Name == "" {
		imageName = fmt.Sprintf("%s/%s:latest", c.PromotionConfiguration.Namespace, string(image.To))
	}

	imageRef := &ImageRef{
		Name:           imageName,
		Namespace:      c.PromotionConfiguration.Namespace,
		ImageStreamRef: c.PromotionConfiguration.Name,
		Branches:       []BranchRef{{ID: branchID}},
	}

	if isInternalBaseImage(string(image.From)) {
		imageRef.FromRoot = true
	} else if string(image.From) != "" {
		fromImage, ok := c.BaseImages[string(image.From)]
		if ok {
			imageRef.Parent = &ImageRef{
				Name:           fromImage.ISTagName(),
				ImageStreamRef: fromImage.Name,
				Namespace:      fromImage.Namespace,
			}

			if v, ok := o.images[fromImage.ISTagName()]; ok {
				imageRef.Parent.ID = v
			}
		}
	}

	if id, ok := o.images[imageName]; ok {
		if err := o.updateImageRef(imageRef, id); err != nil {
			return err
		}
		return nil
	}

	if err := o.addImageRef(imageRef); err != nil {
		return err
	}
	return nil
}

func (o *Operator) addImageRef(image *ImageRef) error {
	logrus.WithField("image", image.Name).Info("Adding image...")

	var m struct {
		AddImage struct {
			NumUIDs int `graphql:"numUids"`
			Image   []struct {
				ID string `graphql:"id"`
			} `graphql:"image"`
		} `graphql:"addImage(input: $input)"`
	}

	type AddImageInput map[string]interface{}
	input := AddImageInput{
		"name":           image.Name,
		"namespace":      image.Namespace,
		"imageStreamRef": image.ImageStreamRef,
		"fromRoot":       image.FromRoot,
	}

	if image.Source != "" {
		input["source"] = image.Source
	}

	if len(image.Branches) > 0 {
		input["branches"] = map[string]interface{}{
			"id": image.Branches[0].ID,
		}
	}

	if image.Parent != nil {
		p := map[string]interface{}{
			"name":           image.Parent.Name,
			"imageStreamRef": image.Parent.ImageStreamRef,
			"namespace":      image.Parent.Namespace,
			"fromRoot":       image.Parent.FromRoot,
		}

		if image.Parent.Source != "" {
			p["source"] = image.Parent.Source
		}

		if v, ok := o.images[image.Parent.Name]; ok {
			p["id"] = v
		}
		input["parent"] = p
	}

	vars := map[string]interface{}{
		"input": []AddImageInput{input},
	}

	if err := o.c.Mutate(context.Background(), &m, vars); err != nil {
		return err
	}

	if len(m.AddImage.Image) > 0 {
		o.images[image.Name] = m.AddImage.Image[0].ID
	}

	return nil
}

func (o *Operator) updateImageRef(newImage *ImageRef, id string) error {
	logrus.WithField("id", id).WithField("image", newImage.Name).Info("Updating image...")
	var m struct {
		UpdateImage struct {
			NumUIDs int `graphql:"numUids"`
		} `graphql:"updateImage(input: $input)"`
	}

	type ImagePatch map[string]interface{}
	type ImageFilter map[string]interface{}
	type UpdateImageInput map[string]interface{}

	patch := ImagePatch{
		"name":           newImage.Name,
		"imageStreamRef": newImage.ImageStreamRef,
		"namespace":      newImage.Namespace,
		"fromRoot":       newImage.FromRoot,
	}

	if newImage.Source != "" {
		patch["source"] = newImage.Source
	}
	if len(newImage.Branches) > 0 {
		patch["branches"] = map[string]interface{}{
			"id": newImage.Branches[0].ID,
		}
	}

	if newImage.Parent != nil {
		p := map[string]interface{}{
			"name":           newImage.Parent.Name,
			"imageStreamRef": newImage.Parent.ImageStreamRef,
			"namespace":      newImage.Parent.Namespace,
			"fromRoot":       newImage.Parent.FromRoot,
		}

		if newImage.Source != "" {
			p["source"] = newImage.Parent.Source
		}

		if v, ok := o.images[newImage.Parent.Name]; ok {
			p["id"] = v
		}

		patch["parent"] = p
	}

	vars := map[string]interface{}{"input": UpdateImageInput{"set": patch, "filter": ImageFilter{"id": id}}}
	if err := o.c.Mutate(context.Background(), &m, vars); err != nil {
		return err
	}
	return nil
}

func isInternalBaseImage(name string) bool {
	return name == "root" || name == "src" || name == "bin"
}

func (o *Operator) loadImages() error {
	if o.images == nil {
		o.images = make(map[string]string)
	}

	var m struct {
		QueryImage []struct {
			ID   string `graphql:"id"`
			Name string `graphql:"name"`
		} `graphql:"queryImage"`
	}

	if err := o.c.Query(context.Background(), &m, nil); err != nil {
		return err
	}

	for _, image := range m.QueryImage {
		o.images[image.Name] = image.ID
	}
	return nil
}
