package rnntest

import "testing"

func TestDemoBlockGradients(t *testing.T) {
	test := GradientTest{
		Block:          NewDemoBlock(3, 2, 4),
		GradientParams: gradientTestVariables,
		Inputs:         gradientTestVariables[:2],
		InStates:       gradientTestVariables[2:4],
	}
	test.Run(t)
	test.GradientParams = nil
	test.Run(t)
}
