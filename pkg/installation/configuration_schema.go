package installation

import (
	"reflect"

	"github.com/swaggest/jsonschema-go"
	"github.com/swaggest/refl"
)

func configurationReflector() jsonschema.Reflector {
	reflector := jsonschema.Reflector{DefaultOptions: []func(*jsonschema.ReflectContext){
		jsonschema.InterceptProp(func(p jsonschema.InterceptPropParams) error {
			if p.Processed && p.PropertySchema.Items != nil {
				return refl.PopulateFieldsFromTags(p.PropertySchema.Items.SchemaOrBool.TypeObject, p.Field.Tag, func(o *refl.FieldsFromTagsOptions) {
					o.TagPrefix = "items."
				})
			}
			return nil
		}),
		jsonschema.InterceptSchema(func(p jsonschema.InterceptSchemaParams) (bool, error) {
			if !p.Processed {
				return false, nil
			}
			t := refl.DeepIndirect(p.Value.Type())
			if t.Kind() == reflect.Struct && (t.Name() != "" || len(p.Schema.Properties) == 0) {
				p.Schema.AdditionalProperties = &jsonschema.SchemaOrBool{TypeBoolean: new(false)}
			}
			return false, nil
		}),
	}}
	return reflector
}
