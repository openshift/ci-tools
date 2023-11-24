package imagegraphgenerator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"

	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

var (
	coreScheme   = scheme.Scheme
	codecFactory = serializer.NewCodecFactory(coreScheme)
)

func init() {
	utilruntime.Must(imagev1.AddToScheme(coreScheme))
	utilruntime.Must(buildv1.AddToScheme(coreScheme))
}

func (o *Operator) loadManifests(path string) error {
	err := filepath.Walk(path,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if ext := filepath.Ext(info.Name()); ext != ".yaml" && ext != ".yml" {
				return nil
			}

			d, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			// The YAML file could hold multiple objects separated by `---`
			dataList := strings.Split(string(d), "---")
			for _, data := range dataList {
				requiredObj, _ := runtime.Decode(codecFactory.UniversalDecoder(corev1.SchemeGroupVersion), []byte(data))
				list, ok := requiredObj.(*corev1.List)
				if ok {
					for _, object := range list.Items {
						o.importObject(object.Raw)
					}
				} else {
					o.importObject([]byte(data))
				}

			}
			return nil
		})
	if err != nil {
		return err
	}
	return nil
}

func (o *Operator) importObject(object []byte) {
	isObject, _ := runtime.Decode(codecFactory.UniversalDecoder(imagev1.SchemeGroupVersion), object)
	if is, ok := isObject.(*imagev1.ImageStream); ok {
		o.imageStreams = append(o.imageStreams, *is)
		return
	}

	bcObject, _ := runtime.Decode(codecFactory.UniversalDecoder(buildv1.SchemeGroupVersion), object)
	if bc, ok := bcObject.(*buildv1.BuildConfig); ok {
		o.buildConfigs = append(o.buildConfigs, *bc)
		return
	}
}

func (o *Operator) AddManifestImages() error {
	for _, is := range o.imageStreams {
		for _, tag := range is.Spec.Tags {
			fullname := fmt.Sprintf("%s/%s:%s", is.Namespace, is.Name, tag.Name)
			imageRef := &ImageRef{
				Name:           fullname,
				Namespace:      is.Namespace,
				ImageStreamRef: is.Name,
			}
			if tag.From != nil {
				imageRef.Source = tag.From.Name
			}
			if id, ok := o.images[fullname]; !ok {
				if err := o.addImageRef(imageRef); err != nil {
					return err
				}
			} else {
				if err := o.updateImageRef(imageRef, id); err != nil {
					return err
				}
			}
		}

	}

	for _, bc := range o.buildConfigs {
		namespace := bc.Spec.Output.To.Namespace
		if namespace == "" {
			namespace = bc.Namespace
		}
		name := fmt.Sprintf("%s/%s", namespace, bc.Spec.Output.To.Name)

		imageRef := &ImageRef{
			Name:           name,
			Namespace:      namespace,
			ImageStreamRef: bc.Spec.Output.To.Name,
		}

		// The buildConfig can be based on another image. This can be specified under the
		// docker strategy.
		if dockerStrategy := bc.Spec.Strategy.DockerStrategy; dockerStrategy != nil {
			if from := dockerStrategy.From; from != nil {
				var fullName string
				var namespace string

				switch from.Kind {
				case "ImageStreamTag":
					namespace := from.Namespace
					if namespace == "" {
						namespace = bc.Namespace
					}
					fullName = fmt.Sprintf("%s/%s", namespace, from.Name)
				case "DockerImage":
					if strings.HasPrefix(from.Name, api.ServiceDomainAPPCIRegistry) {
						splitted := strings.Split(from.Name, "/")
						namespace = splitted[1]
						name = splitted[2]
					}
				}

				if fullName != "" && namespace != "" {
					parent := &ImageRef{
						Name:           fullName,
						Namespace:      namespace,
						ImageStreamRef: from.Name,
					}

					if _, ok := o.images[name]; !ok {
						if err := o.addImageRef(parent); err != nil {
							return err
						}
					}
					imageRef.Parents = append(imageRef.Parents, *parent)
				}
			}
		}

		if id, ok := o.images[name]; !ok {
			if err := o.addImageRef(imageRef); err != nil {
				return err
			}
		} else {
			if err := o.updateImageRef(imageRef, id); err != nil {
				return err
			}
		}
	}

	return nil
}
