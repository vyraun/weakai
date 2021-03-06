package rnn

import (
	"fmt"

	"github.com/unixpickle/autofunc"
	"github.com/unixpickle/num-analysis/linalg"
	"github.com/unixpickle/serializer"
	"github.com/unixpickle/sgd"
)

// BlockSeqFunc is a SeqFunc which operates by using
// a Block as an RNN and running the RNN on input
// sequences.
type BlockSeqFunc struct {
	Block Block
}

// DeserializeBlockSeqFunc deserializes an BlockSeqFunc
// that was serialized.
func DeserializeBlockSeqFunc(d []byte) (*BlockSeqFunc, error) {
	obj, err := serializer.DeserializeWithType(d)
	if err != nil {
		return nil, err
	}
	block, ok := obj.(Block)
	if !ok {
		return nil, fmt.Errorf("expected Block but got %T", obj)
	}
	return &BlockSeqFunc{Block: block}, nil
}

func (r *BlockSeqFunc) BatchSeqs(seqs [][]autofunc.Result) ResultSeqs {
	res := &BlockSeqFuncOutput{
		StartState: r.Block.StartState(),
		PackedOut:  make([][]linalg.Vector, len(seqs)),
	}

	var t int
	for {
		step := &BlockSeqFuncOutputStep{
			InStateVars: make([]*autofunc.Variable, len(seqs)),
			InputVars:   make([]*autofunc.Variable, len(seqs)),
			Inputs:      make([]autofunc.Result, len(seqs)),
			LaneToOut:   map[int]int{},
		}
		var input BlockInput
		for l, seq := range seqs {
			if len(seq) <= t {
				continue
			}
			step.LaneToOut[l] = len(input.Inputs)
			step.Inputs[l] = seq[t]
			step.InputVars[l] = &autofunc.Variable{Vector: seq[t].Output()}
			step.InStateVars[l] = &autofunc.Variable{Vector: res.StartState.Output()}
			if t > 0 {
				s := res.Steps[t-1]
				step.InStateVars[l].Vector = s.Outputs.States()[s.LaneToOut[l]]
			}
			input.Inputs = append(input.Inputs, step.InputVars[l])
			input.States = append(input.States, step.InStateVars[l])
		}
		if len(step.LaneToOut) == 0 {
			break
		}
		step.Outputs = r.Block.Batch(&input)
		res.Steps = append(res.Steps, step)
		for l, outIdx := range step.LaneToOut {
			res.PackedOut[l] = append(res.PackedOut[l], step.Outputs.Outputs()[outIdx])
		}
		t++
	}

	return res
}

func (r *BlockSeqFunc) BatchSeqsR(rv autofunc.RVector, seqs [][]autofunc.RResult) RResultSeqs {
	res := &BlockSeqFuncROutput{
		StartState: r.Block.StartStateR(rv),
		PackedOut:  make([][]linalg.Vector, len(seqs)),
		RPackedOut: make([][]linalg.Vector, len(seqs)),
	}

	var t int
	for {
		step := &BlockSeqFuncROutputStep{
			InStateVars: make([]*autofunc.RVariable, len(seqs)),
			InputVars:   make([]*autofunc.RVariable, len(seqs)),
			Inputs:      make([]autofunc.RResult, len(seqs)),
			LaneToOut:   map[int]int{},
		}
		var input BlockRInput
		for l, seq := range seqs {
			if len(seq) <= t {
				continue
			}
			step.LaneToOut[l] = len(input.Inputs)
			step.Inputs[l] = seq[t]
			step.InputVars[l] = &autofunc.RVariable{
				Variable:   &autofunc.Variable{Vector: seq[t].Output()},
				ROutputVec: seq[t].ROutput(),
			}
			step.InStateVars[l] = &autofunc.RVariable{
				Variable:   &autofunc.Variable{Vector: res.StartState.Output()},
				ROutputVec: res.StartState.ROutput(),
			}
			if t > 0 {
				s := res.Steps[t-1]
				step.InStateVars[l].Variable.Vector = s.Outputs.States()[s.LaneToOut[l]]
				step.InStateVars[l].ROutputVec = s.Outputs.RStates()[s.LaneToOut[l]]
			}
			input.Inputs = append(input.Inputs, step.InputVars[l])
			input.States = append(input.States, step.InStateVars[l])
		}
		if len(step.LaneToOut) == 0 {
			break
		}
		step.Outputs = r.Block.BatchR(rv, &input)
		res.Steps = append(res.Steps, step)
		for l, outIdx := range step.LaneToOut {
			out := step.Outputs
			res.PackedOut[l] = append(res.PackedOut[l], out.Outputs()[outIdx])
			res.RPackedOut[l] = append(res.RPackedOut[l], out.ROutputs()[outIdx])
		}
		t++
	}

	return res
}

// Parameters returns the underlying block's parameters
// if it implements sgd.Learner, or nil otherwise.
func (r *BlockSeqFunc) Parameters() []*autofunc.Variable {
	if l, ok := r.Block.(sgd.Learner); ok {
		return l.Parameters()
	} else {
		return nil
	}
}

func (r *BlockSeqFunc) SerializerType() string {
	return serializerTypeBlockSeqFunc
}

// Serialize serializes the underlying block if it is
// a serializer.Serializer (and fails otherwise).
func (r *BlockSeqFunc) Serialize() ([]byte, error) {
	s, ok := r.Block.(serializer.Serializer)
	if !ok {
		return nil, fmt.Errorf("type is not a Serializer: %T", r.Block)
	}
	return serializer.SerializeWithType(s)
}

type BlockSeqFuncOutputStep struct {
	// These three variables always have len equal to
	// the number of lanes (some entries may be nil).
	InStateVars []*autofunc.Variable
	InputVars   []*autofunc.Variable
	Inputs      []autofunc.Result

	Outputs BlockOutput

	// LaneToOut maps lane indices to indices in Outputs.
	LaneToOut map[int]int
}

type BlockSeqFuncOutput struct {
	StartState autofunc.Result
	Steps      []*BlockSeqFuncOutputStep
	PackedOut  [][]linalg.Vector
}

func (r *BlockSeqFuncOutput) OutputSeqs() [][]linalg.Vector {
	return r.PackedOut
}

func (r *BlockSeqFuncOutput) Gradient(upstream [][]linalg.Vector, g autofunc.Gradient) {
	numLanes := len(r.PackedOut)
	if len(upstream) != numLanes {
		panic("incorrect upstream dimensions")
	}
	for i, x := range upstream {
		if len(x) != len(r.PackedOut[i]) {
			panic("incorrect upstream dimensions")
		}
	}

	stateUpstreams := make([]linalg.Vector, numLanes)
	for t := len(r.Steps) - 1; t >= 0; t-- {
		step := r.Steps[t]

		var stepUpstream UpstreamGradient
		loopUsedLanes(step.LaneToOut, func(l int) {
			stateVar := step.InStateVars[l]
			u := upstream[l][t]
			stepUpstream.Outputs = append(stepUpstream.Outputs, u)
			s := stateUpstreams[l]
			if s == nil {
				s = make(linalg.Vector, len(stateVar.Vector))
			}
			stepUpstream.States = append(stepUpstream.States, s)
			g[stateVar] = make(linalg.Vector, len(stateVar.Vector))
			if !step.Inputs[l].Constant(g) {
				g[step.InputVars[l]] = make(linalg.Vector, len(step.InputVars[l].Vector))
			}
		})

		step.Outputs.Gradient(&stepUpstream, g)

		loopUsedLanes(step.LaneToOut, func(l int) {
			stateVar := step.InStateVars[l]
			stateUpstreams[l] = g[stateVar]
			delete(g, stateVar)
			if input := step.Inputs[l]; !input.Constant(g) {
				upstream := g[step.InputVars[l]]
				delete(g, step.InputVars[l])
				input.PropagateGradient(upstream, g)
			}
		})
	}
	for _, upstream := range stateUpstreams {
		if upstream != nil {
			r.StartState.PropagateGradient(upstream, g)
		}
	}
}

type BlockSeqFuncROutputStep struct {
	InStateVars []*autofunc.RVariable
	InputVars   []*autofunc.RVariable
	Inputs      []autofunc.RResult

	Outputs BlockROutput

	LaneToOut map[int]int
}

type BlockSeqFuncROutput struct {
	StartState autofunc.RResult
	Steps      []*BlockSeqFuncROutputStep
	PackedOut  [][]linalg.Vector
	RPackedOut [][]linalg.Vector
}

func (r *BlockSeqFuncROutput) OutputSeqs() [][]linalg.Vector {
	return r.PackedOut
}

func (r *BlockSeqFuncROutput) ROutputSeqs() [][]linalg.Vector {
	return r.RPackedOut
}

func (r *BlockSeqFuncROutput) RGradient(upstream, upstreamR [][]linalg.Vector,
	rg autofunc.RGradient, g autofunc.Gradient) {
	// g is used for temporary variables.
	if g == nil {
		g = autofunc.Gradient{}
	}

	numLanes := len(r.PackedOut)
	if len(upstream) != numLanes || len(upstreamR) != numLanes {
		panic("incorrect upstream dimensions")
	}
	for i, x := range r.PackedOut {
		if len(upstream[i]) != len(x) || len(upstreamR[i]) != len(x) {
			panic("incorrect upstream dimensions")
		}
	}

	stateUpstreams := make([]linalg.Vector, numLanes)
	stateRUpstreams := make([]linalg.Vector, numLanes)
	for t := len(r.Steps) - 1; t >= 0; t-- {
		step := r.Steps[t]

		var stepUpstream UpstreamRGradient
		loopUsedLanes(step.LaneToOut, func(l int) {
			stateVar := step.InStateVars[l].Variable
			u := upstream[l][t]
			uR := upstreamR[l][t]
			stepUpstream.Outputs = append(stepUpstream.Outputs, u)
			stepUpstream.ROutputs = append(stepUpstream.ROutputs, uR)
			s := stateUpstreams[l]
			sR := stateRUpstreams[l]
			if s == nil {
				s = make(linalg.Vector, len(stateVar.Vector))
				sR = make(linalg.Vector, len(stateVar.Vector))
			}
			stepUpstream.States = append(stepUpstream.States, s)
			stepUpstream.RStates = append(stepUpstream.RStates, sR)
			g[stateVar] = make(linalg.Vector, len(stateVar.Vector))
			rg[stateVar] = make(linalg.Vector, len(stateVar.Vector))
			if !step.Inputs[l].Constant(rg, g) {
				v := step.InputVars[l].Variable
				g[v] = make(linalg.Vector, len(v.Vector))
				rg[v] = make(linalg.Vector, len(v.Vector))
			}
		})

		step.Outputs.RGradient(&stepUpstream, rg, g)

		loopUsedLanes(step.LaneToOut, func(l int) {
			stateVar := step.InStateVars[l].Variable
			stateUpstreams[l] = g[stateVar]
			stateRUpstreams[l] = rg[stateVar]
			delete(g, stateVar)
			delete(rg, stateVar)
			if input := step.Inputs[l]; !input.Constant(rg, g) {
				v := step.InputVars[l].Variable
				upstream := g[v]
				upstreamR := rg[v]
				delete(g, v)
				delete(rg, v)
				input.PropagateRGradient(upstream, upstreamR, rg, g)
			}
		})
	}

	for i, upstream := range stateUpstreams {
		if upstream != nil {
			r.StartState.PropagateRGradient(upstream, stateRUpstreams[i], rg, g)
		}
	}
}

func loopUsedLanes(laneToOut map[int]int, f func(int)) {
	var lane int
	k := len(laneToOut)
	for k > 0 {
		if _, ok := laneToOut[lane]; ok {
			f(lane)
			k--
		}
		lane++
	}
}
