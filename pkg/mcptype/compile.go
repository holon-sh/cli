package mcptype

import (
	"context"
	"errors"
)

// catalog is one binding's compiled tools.
type catalog struct {
	tools map[string]compiledTool
}

type compiledTool struct {
	input, output shape
}

func parseCatalog(ctx context.Context, contract Contract) (catalog, error) {
	result := catalog{tools: make(map[string]compiledTool, len(contract.Tools))}
	for _, name := range sortedKeys(contract.Tools) {
		tool := contract.Tools[name]
		input, err := parseShape(ctx, tool.Input)
		if err != nil {
			return catalog{}, err
		}
		output := shape{named: map[string]typ{}}
		if tool.Output != nil {
			if output, err = parseShape(ctx, tool.Output); err != nil {
				return catalog{}, err
			}
		}
		result.tools[name] = compiledTool{input: input, output: output}
	}
	return result, nil
}

// compileCatalogs validates the binding names and compiles every contract's schemas.
func compileCatalogs(ctx context.Context, contracts map[string]Contract) (map[string]catalog, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	catalogs := make(map[string]catalog, len(contracts))
	for _, name := range sortedKeys(contracts) {
		if name == "" {
			return nil, errors.New("binding names must be nonempty")
		}
		compiled, err := parseCatalog(ctx, contracts[name])
		if err != nil {
			return nil, err
		}
		catalogs[name] = compiled
	}
	return catalogs, nil
}
