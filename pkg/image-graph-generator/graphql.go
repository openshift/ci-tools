package imagegraphgenerator

type AddImagePayload struct {
	AddImage AddImage `graphql:"addImage(input: $input)"`
}

type AddImage struct {
	NumUIDs int            `graphql:"numUids"`
	Image   []ImagePayload `graphql:"image"`
}

type ImagePayload struct {
	ID string `graphql:"id"`
}

type ImageInput struct {
	Input []Image `json:"input,omitempty"`
}

type Image struct {
	ID             string                 `json:"id,omitempty"`
	Name           string                 `json:"name,omitempty"`
	ImageStreamRef string                 `json:"imageStreamRef,omitempty"`
	Namespace      string                 `json:"namespace,omitempty"`
	FromRoot       bool                   `json:"fromRoot,omitempty"`
	Source         string                 `json:"source,omitempty"`
	Branches       map[string]interface{} `json:"branches,omitempty"`
	Parent         *Image                 `json:"parent,omitempty"`
	Children       []Image                `json:"children,omitempty"`
}

type AddImageInput map[string]interface{}
type UpdateImageInput map[string]interface{}
type ImagePatch map[string]interface{}
type ImageFilter map[string]interface{}
