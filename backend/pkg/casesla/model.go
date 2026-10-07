package casesla

import "shopee/backend/pkg/casesla/deadline"

type Item = deadline.Item
type StageInput = deadline.StageInput

const PolicyVersion = deadline.PolicyVersion

func New(in StageInput) Item { return deadline.New(in) }
