package yaml

type Marshaler func(interface{}) ([]byte, error)

func MarshalMultidoc(marshaler Marshaler, objs ...interface{}) ([]byte, error) {
	yamlBytes := []byte{}
	l := len(objs) - 1
	delim := []byte("---\n")
	for i, obj := range objs {
		bytes, err := marshaler(obj)
		if err != nil {
			return nil, err
		}
		yamlBytes = append(yamlBytes, bytes...)
		if i < l {
			yamlBytes = append(yamlBytes, delim...)
		}
	}
	return yamlBytes, nil
}
