package adapter

import "shopee/backend/pkg/cartselection"

// SelectCartLines never enriches unselected lines or silently drops an unknown ID.
func SelectCartLines(lines []CartLine, selected []string) ([]CartLine, error) {
	if selected == nil {
		return lines, nil
	}
	if err := cartselection.Validate(selected); err != nil {
		return nil, err
	}
	byID := make(map[string]CartLine, len(lines))
	for _, line := range lines {
		byID[line.LineID] = line
	}
	out := make([]CartLine, 0, len(selected))
	for _, id := range selected {
		line, ok := byID[id]
		if !ok {
			return nil, cartselection.Invalid()
		}
		out = append(out, line)
	}
	return out, nil
}
