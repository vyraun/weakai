package rnn

import (
	"fmt"

	"github.com/unixpickle/autofunc"
	"github.com/unixpickle/num-analysis/linalg"
)

// BatcherBlock turns an autofunc.RBatcher into a Block.
//
// The inputs and outputs of the RBatcher are packed by
// appending the state to the input/output.
// For example, an input of {1,2} and a state of {3,4}
// would be packed as {1,2,3,4}.
type BatcherBlock struct {
	F            autofunc.RBatcher
	StateSizeVal int

	StartStateVar *autofunc.Variable
}

// StateSize returns the state size determined by
// b.StateSizeVal.
func (b *BatcherBlock) StateSize() int {
	return b.StateSizeVal
}

// StartState returns the initial state, as given by
// b.StartStateVar.
// If b.StartStateVar is nil, then a state filled with
// zeroes is returned.
func (b *BatcherBlock) StartState() autofunc.Result {
	if b.StartStateVar != nil {
		return b.StartStateVar
	}
	return &autofunc.Variable{Vector: make(linalg.Vector, b.StateSizeVal)}
}

// StartStateR is like StartState but with r-operators.
func (b *BatcherBlock) StartStateR(rv autofunc.RVector) autofunc.RResult {
	if b.StartStateVar != nil {
		return autofunc.NewRVariable(b.StartStateVar, rv)
	}
	initVec := make(linalg.Vector, b.StateSize())
	return &autofunc.RVariable{
		Variable:   &autofunc.Variable{Vector: initVec},
		ROutputVec: initVec,
	}
}

// Batch applies the block to each of the inputs.
func (b *BatcherBlock) Batch(in *BlockInput) BlockOutput {
	joined := joinBlockInput(in)
	output := b.F.Batch(joined, len(in.States))
	return &batcherBlockOutput{
		Result:    output,
		StateSize: b.StateSizeVal,
		LaneCount: len(in.States),
	}
}

// BatchR applies the block to each of the inputs.
func (b *BatcherBlock) BatchR(v autofunc.RVector, in *BlockRInput) BlockROutput {
	joined := joinBlockRInput(in)
	output := b.F.BatchR(v, joined, len(in.States))
	return &batcherBlockOutput{
		RResult:   output,
		StateSize: b.StateSizeVal,
		LaneCount: len(in.States),
	}
}

type batcherBlockOutput struct {
	Result    autofunc.Result
	RResult   autofunc.RResult
	StateSize int
	LaneCount int
}

func (b *batcherBlockOutput) States() []linalg.Vector {
	return b.extractStates(b.resultOutputVector())
}

func (b *batcherBlockOutput) RStates() []linalg.Vector {
	return b.extractStates(b.RResult.ROutput())
}

func (b *batcherBlockOutput) Outputs() []linalg.Vector {
	return b.extractOutputs(b.resultOutputVector())
}

func (b *batcherBlockOutput) ROutputs() []linalg.Vector {
	return b.extractOutputs(b.RResult.ROutput())
}

func (b *batcherBlockOutput) Gradient(u *UpstreamGradient, g autofunc.Gradient) {
	upstreamVec := b.joinUpstream(u.States, u.Outputs)
	b.Result.PropagateGradient(upstreamVec, g)
}

func (b *batcherBlockOutput) RGradient(u *UpstreamRGradient, rg autofunc.RGradient,
	g autofunc.Gradient) {
	upstreamVec := b.joinUpstream(u.States, u.Outputs)
	rupstreamVec := b.joinUpstream(u.RStates, u.ROutputs)
	b.RResult.PropagateRGradient(upstreamVec, rupstreamVec, rg, g)
}

func (b *batcherBlockOutput) extractStates(output linalg.Vector) []linalg.Vector {
	outSize := b.outputSize()
	res := make([]linalg.Vector, b.LaneCount)
	for i := range res {
		startIdx := i*(outSize+b.StateSize) + outSize
		res[i] = output[startIdx : startIdx+b.StateSize]
	}
	return res
}

func (b *batcherBlockOutput) extractOutputs(output linalg.Vector) []linalg.Vector {
	outSize := b.outputSize()
	res := make([]linalg.Vector, b.LaneCount)
	for i := range res {
		startIdx := i * (outSize + b.StateSize)
		res[i] = output[startIdx : startIdx+outSize]
	}
	return res
}

func (b *batcherBlockOutput) joinUpstream(states, outputs []linalg.Vector) linalg.Vector {
	outputSize := b.outputSize()
	if states == nil {
		for _ = range outputs {
			states = append(states, make(linalg.Vector, b.StateSize))
		}
	} else if outputs == nil {
		for _ = range states {
			outputs = append(outputs, make(linalg.Vector, outputSize))
		}
	}

	upstreamVec := make(linalg.Vector, (len(states[0])+len(outputs[0]))*len(states))
	var idx int
	for i, output := range outputs {
		if len(output) != outputSize {
			panic(fmt.Sprintf("output should have len %d but has len %d",
				outputSize, len(output)))
		}
		if len(states[i]) != b.StateSize {
			panic(fmt.Sprintf("state should have len %d but has len %d",
				b.StateSize, len(states[i])))
		}
		copy(upstreamVec[idx:], output)
		idx += len(output)
		state := states[i]
		copy(upstreamVec[idx:], state)
		idx += len(state)
	}
	return upstreamVec
}

func (b *batcherBlockOutput) outputSize() int {
	return len(b.resultOutputVector())/b.LaneCount - b.StateSize
}

func (b *batcherBlockOutput) resultOutputVector() linalg.Vector {
	if b.Result != nil {
		return b.Result.Output()
	} else {
		return b.RResult.Output()
	}
}
