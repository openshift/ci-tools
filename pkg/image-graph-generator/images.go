package imagegraphgenerator

import (
	"context"
	"fmt"
	"regexp"

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
	Parents        []ImageRef  `graphql:"parents"`
	Children       []ImageRef  `graphql:"children"`
}

func (o *Operator) UpdateImage(image api.ProjectDirectoryImageBuildStepConfiguration, c *api.ReleaseBuildConfiguration, branchID string) error {
	imageName := fmt.Sprintf("%s/%s:%s", c.PromotionConfiguration.Namespace, c.PromotionConfiguration.Name, string(image.To))

	if c.PromotionConfiguration.Name == "" {
		if c.PromotionConfiguration.Tag == "" {
			imageName = fmt.Sprintf("%s/%s:latest", c.PromotionConfiguration.Namespace, string(image.To))
		} else {
			imageName = fmt.Sprintf("%s/%s:%s", c.PromotionConfiguration.Namespace, c.PromotionConfiguration.Tag, string(image.To))
		}
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
			parent := ImageRef{
				Name:           fromImage.ISTagName(),
				ImageStreamRef: fromImage.Name,
				Namespace:      fromImage.Namespace,
			}

			if v, ok := o.images[fromImage.ISTagName()]; ok {
				parent.ID = v
			}

			imageRef.Parents = append(imageRef.Parents, parent)
		}
	}

	if image.ProjectDirectoryImageBuildInputs.Inputs != nil {
		for _, imageInput := range image.ProjectDirectoryImageBuildInputs.Inputs {
			for _, as := range imageInput.As {
				imageInfo := extractImageFromURL(as)
				if imageInfo == nil {
					continue
				}
				fullName := fmt.Sprintf("%s/%s:%s", imageInfo.namespace, imageInfo.name, imageInfo.tag)
				parent := ImageRef{
					Name:           fullName,
					ImageStreamRef: imageInfo.name,
					Namespace:      imageInfo.namespace,
				}

				if v, ok := o.images[fullName]; ok {
					parent.ID = v
				}

				imageRef.Parents = append(imageRef.Parents, parent)
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

	var parents []interface{}
	for _, parent := range image.Parents {
		p := map[string]interface{}{
			"name":           parent.Name,
			"imageStreamRef": parent.ImageStreamRef,
			"namespace":      parent.Namespace,
			"fromRoot":       parent.FromRoot,
		}

		if parent.Source != "" {
			p["source"] = parent.Source
		}

		if v, ok := o.images[parent.Name]; ok {
			p["id"] = v
		}
		parents = append(parents, p)
	}

	if len(parents) > 0 {
		input["parents"] = parents
	}

	vars := map[string]interface{}{
		"input": []AddImageInput{input},
	}

	var m AddImagePayload
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

	var parents []interface{}
	for _, parent := range newImage.Parents {
		p := map[string]interface{}{
			"name":           parent.Name,
			"imageStreamRef": parent.ImageStreamRef,
			"namespace":      parent.Namespace,
			"fromRoot":       parent.FromRoot,
		}

		if newImage.Source != "" {
			p["source"] = parent.Source
		}

		if v, ok := o.images[parent.Name]; ok {
			p["id"] = v
		}

		parents = append(parents, p)
	}

	if len(parents) > 0 {
		patch["parents"] = parents
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

type imageInfo struct {
	registry  string
	namespace string
	name      string
	tag       string
}

func extractImageFromURL(imageURL string) *imageInfo {
	re := regexp.MustCompile(`^(.+?)/(.+?)/(.+?):(.+)$`)
	matches := re.FindStringSubmatch(imageURL)
	if matches == nil {
		return nil
	}
	registry := matches[1]
	namespace := matches[2]
	imageName := matches[3]
	tag := matches[4]
	return &imageInfo{
		registry:  registry,
		namespace: namespace,
		name:      imageName,
		tag:       tag,
	}
}
