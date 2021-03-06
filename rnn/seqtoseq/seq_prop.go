package seqtoseq

import (
	"github.com/unixpickle/autofunc"
	"github.com/unixpickle/num-analysis/linalg"
	"github.com/unixpickle/weakai/neuralnet"
	"github.com/unixpickle/weakai/rnn"
)

type seqPropStep struct {
	StartState autofunc.Result
	Output     rnn.BlockOutput
	InStates   []*autofunc.Variable
	InSeqs     []Sample
}

type seqRPropStep struct {
	StartState autofunc.RResult
	Output     rnn.BlockROutput
	InStates   []*autofunc.RVariable
	InSeqs     []Sample
}

// seqProp facilitates back propagation through sequences.
type seqProp struct {
	Block    rnn.Block
	CostFunc neuralnet.CostFunc

	memory []*seqPropStep
}

// TimeStep evaluates the RNN block on the first input
// of each sequence in the batch.
// None of the sequences may be empty unless this is
// the first timestep.
//
// It returns a new batch of sequences by removing the
// first time step from all of the existing sequences.
// The output batch may be smaller than the input batch,
// since sequences with no more time steps are removed.
func (s *seqProp) TimeStep(inSeqs []Sample) []Sample {
	if s.MemoryCount() == 0 {
		inSeqs = removeEmpty(inSeqs)
	}
	if len(inSeqs) == 0 {
		return nil
	}
	input := s.headInput(inSeqs)
	output := s.Block.Batch(input)
	step := &seqPropStep{
		Output:   output,
		InStates: input.States,
		InSeqs:   inSeqs,
	}
	if s.MemoryCount() == 0 {
		step.StartState = s.Block.StartState()
	}
	s.memory = append(s.memory, step)
	return removeFirst(inSeqs)
}

// MemoryCount returns the number of time steps with
// information stored in memory (i.e. the maximum #
// of time steps to back-propagate through).
func (s *seqProp) MemoryCount() int {
	return len(s.memory)
}

// Truncate removes all memory about older timesteps so
// that no more than the last n timesteps are in memory.
//
// Truncating to a length of zero is equivalent to
// resetting the seqProp.
func (s *seqProp) Truncate(n int) {
	removeCount := s.MemoryCount() - n
	if removeCount <= 0 {
		return
	}

	copy(s.memory, s.memory[removeCount:])

	// Allow GC to collect some removed items.
	for i := 0; i < removeCount; i++ {
		s.memory[i+n] = nil
	}

	s.memory = s.memory[:n]
}

// BackPropagate propagates backward in time using the
// given head size and tail size.
// The head size indicates for how many time steps back
// the outputs should be considered, and the tail size
// indicates how many more timesteps back BPTT should go
// than the head size (i.e. to propagate through previous
// states, but not outputs).
func (s *seqProp) BackPropagate(g autofunc.Gradient, headSize, tailSize int) {
	if headSize == 0 || s.MemoryCount() == 0 {
		return
	}
	upstream := &rnn.UpstreamGradient{}
	lowIdx := s.MemoryCount() - (headSize + tailSize)
	lowHead := s.MemoryCount() - headSize
	if lowIdx < 0 {
		lowIdx = 0
	}
	getInitialState := s.memory[lowIdx].StartState != nil
	for i := s.MemoryCount() - 1; i >= lowIdx; i-- {
		mem := s.memory[i]

		upstream.Outputs = nil
		if i >= lowHead {
			for lane, output := range mem.Output.Outputs() {
				desiredOut := mem.InSeqs[lane].Outputs[0]
				outGrad := costFuncDeriv(s.CostFunc, desiredOut, output)
				upstream.Outputs = append(upstream.Outputs, outGrad)
			}
		}

		var stateGrads []linalg.Vector
		if i > lowIdx || getInitialState {
			for _, state := range mem.InStates {
				x := make(linalg.Vector, s.Block.StateSize())
				g[state] = x
				stateGrads = append(stateGrads, x)
			}
		}

		mem.Output.Gradient(upstream, g)

		if len(stateGrads) > 0 {
			for _, state := range mem.InStates {
				delete(g, state)
			}
			if i > 0 {
				lastSeqs := s.memory[i-1].InSeqs
				upstream.States = injectDiscontinued(lastSeqs, stateGrads,
					s.Block.StateSize())
			} else {
				upstream.States = stateGrads
			}
		}
	}
	if getInitialState {
		startState := s.memory[0].StartState
		for _, stateUpstream := range upstream.States {
			if stateUpstream != nil {
				startState.PropagateGradient(stateUpstream, g)
			}
		}
	}
}

func (s *seqProp) headInput(seqs []Sample) *rnn.BlockInput {
	var lastStates []linalg.Vector
	if s.MemoryCount() > 0 {
		lastBlock := s.memory[len(s.memory)-1]
		lastStates = filterContinued(lastBlock.InSeqs, lastBlock.Output.States())
		if len(lastStates) != len(seqs) {
			panic("incorrect number of input sequences")
		}
	} else {
		initState := s.Block.StartState().Output()
		for i := 0; i < len(seqs); i++ {
			lastStates = append(lastStates, initState)
		}
	}

	input := &rnn.BlockInput{}
	for lane, seq := range seqs {
		inVar := &autofunc.Variable{Vector: seq.Inputs[0]}
		input.Inputs = append(input.Inputs, inVar)
		inState := &autofunc.Variable{Vector: lastStates[lane]}
		input.States = append(input.States, inState)
	}
	return input
}

// seqRProp is like seqProp, but for the R operator.
type seqRProp struct {
	Block    rnn.Block
	CostFunc neuralnet.CostFunc

	memory []*seqRPropStep
}

func (s *seqRProp) TimeStep(v autofunc.RVector, inSeqs []Sample) []Sample {
	if s.MemoryCount() == 0 {
		inSeqs = removeEmpty(inSeqs)
	}
	if len(inSeqs) == 0 {
		return nil
	}
	input := s.headInput(v, inSeqs)
	output := s.Block.BatchR(v, input)
	step := &seqRPropStep{
		Output:   output,
		InStates: input.States,
		InSeqs:   inSeqs,
	}
	if s.MemoryCount() == 0 {
		step.StartState = s.Block.StartStateR(v)
	}
	s.memory = append(s.memory, step)
	return removeFirst(inSeqs)
}

func (s *seqRProp) MemoryCount() int {
	return len(s.memory)
}

func (s *seqRProp) Truncate(n int) {
	removeCount := s.MemoryCount() - n
	if removeCount <= 0 {
		return
	}
	copy(s.memory, s.memory[removeCount:])
	for i := 0; i < removeCount; i++ {
		s.memory[i+n] = nil
	}
	s.memory = s.memory[:n]
}

func (s *seqRProp) BackPropagate(g autofunc.Gradient, rg autofunc.RGradient,
	headSize, tailSize int) {
	if headSize == 0 || s.MemoryCount() == 0 {
		return
	}
	upstream := &rnn.UpstreamRGradient{}
	lowIdx := s.MemoryCount() - (headSize + tailSize)
	lowHead := s.MemoryCount() - headSize
	if lowIdx < 0 {
		lowIdx = 0
	}
	getInitialState := s.memory[lowIdx].StartState != nil
	for i := s.MemoryCount() - 1; i >= lowIdx; i-- {
		mem := s.memory[i]

		upstream.Outputs = nil
		upstream.ROutputs = nil
		if i >= lowHead {
			rout := mem.Output.ROutputs()
			for lane, output := range mem.Output.Outputs() {
				desiredOut := mem.InSeqs[lane].Outputs[0]
				d, rd := costFuncRDeriv(s.CostFunc, desiredOut, output, rout[lane])
				upstream.Outputs = append(upstream.Outputs, d)
				upstream.ROutputs = append(upstream.ROutputs, rd)
			}
		}

		var stateGrads []linalg.Vector
		var stateRGrads []linalg.Vector
		if i > lowIdx || getInitialState {
			for _, state := range mem.InStates {
				grad := make(linalg.Vector, s.Block.StateSize())
				rgrad := make(linalg.Vector, s.Block.StateSize())
				g[state.Variable] = grad
				rg[state.Variable] = rgrad
				stateGrads = append(stateGrads, grad)
				stateRGrads = append(stateRGrads, rgrad)
			}
		}

		mem.Output.RGradient(upstream, rg, g)

		if len(stateGrads) > 0 {
			for _, state := range mem.InStates {
				delete(g, state.Variable)
				delete(rg, state.Variable)
			}
			if i > 0 {
				lastSeqs := s.memory[i-1].InSeqs
				upstream.States = injectDiscontinued(lastSeqs, stateGrads,
					s.Block.StateSize())
				upstream.RStates = injectDiscontinued(lastSeqs, stateRGrads,
					s.Block.StateSize())
			} else {
				upstream.States = stateGrads
				upstream.RStates = stateRGrads
			}
		}
	}
	if getInitialState {
		startState := s.memory[0].StartState
		for i, stateUpstream := range upstream.States {
			if stateUpstream != nil {
				startState.PropagateRGradient(stateUpstream, upstream.RStates[i], rg, g)
			}
		}
	}
}

func (s *seqRProp) headInput(rv autofunc.RVector, seqs []Sample) *rnn.BlockRInput {
	var lastStates []linalg.Vector
	var lastRStates []linalg.Vector
	if s.MemoryCount() > 0 {
		lastBlock := s.memory[len(s.memory)-1]
		lastStates = filterContinued(lastBlock.InSeqs, lastBlock.Output.States())
		lastRStates = filterContinued(lastBlock.InSeqs, lastBlock.Output.RStates())
		if len(lastStates) != len(seqs) {
			panic("incorrect number of input sequences")
		}
	} else {
		startState := s.Block.StartStateR(rv)
		for i := 0; i < len(seqs); i++ {
			lastStates = append(lastStates, startState.Output())
			lastRStates = append(lastRStates, startState.ROutput())
		}
	}
	input := &rnn.BlockRInput{}
	zeroInRVec := make(linalg.Vector, len(seqs[0].Inputs[0]))
	for lane, seq := range seqs {
		inVar := &autofunc.Variable{Vector: seq.Inputs[0]}
		inRVar := &autofunc.RVariable{
			Variable:   inVar,
			ROutputVec: zeroInRVec,
		}
		input.Inputs = append(input.Inputs, inRVar)
		inState := &autofunc.Variable{Vector: lastStates[lane]}
		inRState := &autofunc.RVariable{
			Variable:   inState,
			ROutputVec: lastRStates[lane],
		}
		input.States = append(input.States, inRState)
	}
	return input
}

func removeEmpty(seqs []Sample) []Sample {
	var res []Sample
	for _, seq := range seqs {
		if len(seq.Inputs) != 0 {
			res = append(res, seq)
		}
	}
	return res
}

func removeFirst(seqs []Sample) []Sample {
	var nextSeqs []Sample
	for _, seq := range seqs {
		if len(seq.Inputs) == 1 {
			continue
		}
		s := Sample{Inputs: seq.Inputs[1:], Outputs: seq.Outputs[1:]}
		nextSeqs = append(nextSeqs, s)
	}
	return nextSeqs
}

// filterContinued filters ins so that input i
// is only kept if the i-th sequence has more
// than one input in it.
func filterContinued(seqs []Sample, ins []linalg.Vector) []linalg.Vector {
	var res []linalg.Vector
	for i, seq := range seqs {
		if len(seq.Inputs) > 1 {
			res = append(res, ins[i])
		}
	}
	return res
}

// injectDiscontinued injects zeroed slices in
// a result for every element of seqs which has
// less than two inputs.
func injectDiscontinued(seqs []Sample, outs []linalg.Vector, vecLen int) []linalg.Vector {
	var zeroVec linalg.Vector
	var res []linalg.Vector
	var takeIdx int
	for _, s := range seqs {
		if len(s.Inputs) > 1 {
			res = append(res, outs[takeIdx])
			takeIdx++
		} else {
			if zeroVec == nil {
				zeroVec = make(linalg.Vector, vecLen)
			}
			res = append(res, zeroVec)
		}
	}
	return res
}
