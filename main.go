package main

import (
	"fmt"
	"image/color"
	"math"
	"math/rand"
	"sort"
	"time"

	exprand "golang.org/x/exp/rand"
	"gonum.org/v1/gonum/stat/distuv"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func main() {

}

// We'll use this for TAB score generation for each block.
// A normal distribution may not be the best fit. TODO.
var normalDist = distuv.Normal{
	Mu:    float64(genesisBlockTABS),
	Sigma: float64(genesisBlockTABS) / 4, // I just made this up. TODO.
	Src:   exprand.NewSource(uint64(time.Now().UnixNano())),
}

// Globals
var ticksPerSecond int64 = 10
var tickSamples = ticksPerSecond * int64((time.Hour * 6).Seconds())
var networkLambda = (float64(1) / float64(13)) / float64(ticksPerSecond)
var countMiners = int64(12)
var minerNeighborRate float64 = 0.5 // 0.7
var blockReward int64 = 3

var latencySecondsDefault float64 = 1                  // 1.23               // 2.5
var delaySecondsDefault float64 = 0                    // miner hesitancy to broadcast solution
var receivePostponeSecondsDefault float64 = 100 / 1000 // 80 milliseconds, ish

var tabsAdjustmentDenominator = int64(128) // int64(4096) <-- 4096 is the 'equilibrium' value, lower values prefer richer miners more (devaluing hashrate)
const genesisBlockTABS int64 = 10_000      // tabs starting value
const genesisDifficulty = 10_000_000_000

// presumeMinerShareBalancePerBlockDenominator being 300 means that we assume that a miner's balance accounts for 1/300
// of the overall genesis block TAB score. This implies 300 transactions per block.
// This value is used to set the starting balance for miners.
const presumeMinerShareBalancePerBlockDenominator = 100

var txPoolBlockTABs = make(map[int64]int64)

var genesisBlock = &Block{
	i:         0,
	s:         0,
	d:         genesisDifficulty,
	td:        genesisDifficulty,
	tabs:      genesisBlockTABS,
	ttdtabs:   genesisBlockTABS * genesisDifficulty,
	miner:     "00F00F",
	delay:     Delay{},
	h:         fmt.Sprintf("%08x", rand.Int63()),
	ph:        "00000000",
	canonical: true,
}

type Miners []*Miner

func (ms Miners) headMax() (max int64) {
	for _, m := range ms {
		if m.head.i > max {
			max = m.head.i
		}
	}
	return max
}

type minerEvent struct {
	minerI int
	i      int64
	blocks Blocks
}

type Miner struct {
	Index   int64
	Address string
	Blocks  BlockTree

	Hashrate      float64
	HashesPerTick int64 // per tick
	Balance       int64 // Wei
	BalanceCap    int64 // Max Wei this miner will hold. Use 0 for no limit hold 'em.
	CostPerBlock  int64 // cost to miner, expended after each block win (via tx on text block)

	Latency func() int64

	// SendDelay represents a miner withholding a discovered puzzle solution, ie. "selfish mining"
	SendDelay func(block *Block) int64

	// ReceiveDelay represents a miner's reluctance to mine a latest-available head block.
	// This could be because they are rich and try to produce a block with a higher TABS than a known low-TABS block.
	// This is experimental; is this scheme profitable?
	ReceiveDelay func(block *Block) int64

	ConsensusAlgorithm             ConsensusAlgorithm
	ConsensusArbitrations          int
	ConsensusObjectiveArbitrations int

	// StrategySkipRandom tells the miner whether to skip the final coin toss arbitration.
	// When true, the miner will prefer the first block available to it at that height.
	StrategySkipRandom bool

	reorgs                   map[int64]reorg
	decisionConditionTallies map[string]int

	head *Block

	neighbors      []*Miner
	receivedBlocks map[int64]Blocks

	cord chan minerEvent

	tick int64
}

func getBlockDifficulty(parent *Block, uncles bool, interval int64) int64 {
	x := interval / (9 * ticksPerSecond) // 9 SECONDS
	y := 1 - x
	if uncles {
		y = 2 - x
	}
	if y < -99 {
		y = -99
	}
	return int64(float64(parent.d) + (float64(y) / 2048 * float64(parent.d)))
}

func getTABS(parentTabs, localTAB int64) (tabs int64) {
	scalarNumerator := int64(0)
	if localTAB > parentTabs {
		scalarNumerator = 1
	} else if localTAB < parentTabs {
		scalarNumerator = -1
	}

	numerator := tabsAdjustmentDenominator + scalarNumerator // [127|128|129]/128, [4095|4096|4097]/4096

	return int64(float64(parentTabs) * float64(numerator) / float64(tabsAdjustmentDenominator))
}

func getTABS_step(parentTabs, tabFallCount, localTAB int64) (tabs int64) {
	scalarNumerator := int64(0)
	if localTAB > parentTabs {
		scalarNumerator = 1
	} else if localTAB < parentTabs {
		scalarNumerator = -1 - (tabFallCount / 9) // floor divide
	}

	numerator := tabsAdjustmentDenominator + scalarNumerator // [127|128|129]/128, [4095|4096|4097]/4096

	return int64(float64(parentTabs) * float64(numerator) / float64(tabsAdjustmentDenominator))
}

func (m *Miner) doTick(s int64) {
	m.tick = s

	// Get tick-expired received blocks and process them.
	for k, v := range m.receivedBlocks {
		if m.tick >= k && /* future block inhibition -> */ m.tick+(15*ticksPerSecond) > k {
			// process blocks in order they were received (per time slot)
			for _, b := range v {
				m.processBlock(b)
			}
			delete(m.receivedBlocks, k)
		}
	}

	// Mine.
	m.mineTick()
}

func fakeHashimoto(hashratePerTick, parentDifficulty, networkLambda float64) bool {
	tickR := hashratePerTick / parentDifficulty * networkLambda
	tickR = tickR / 2

	// Do we solve it?
	needle := rand.Float64()
	trial := rand.Float64()

	return math.Abs(trial-needle) <= tickR ||
		math.Abs(trial-needle) >= 1-tickR
}

func (m *Miner) mineTick() {
	parent := m.head

	solved := fakeHashimoto(float64(m.HashesPerTick), float64(parent.d), networkLambda)
	if !solved {
		return
	}

	// Naively, the block tick (timestamp) is the miner's real tick.
	s := m.tick

	// But if the tickInterval allows multiple ticks / second,
	// we need to enforce that the timestamp is a unit-second value.
	s = s / ticksPerSecond // floor
	s = s * ticksPerSecond // back to interval units

	// In order for the block to be valid, the tick must be greater
	// than that of its parent.
	if s == parent.s {
		s = parent.s + 1
	}

	// Get a random value (from a normal distribution) as a representation of this block's TAB.
	// This is a global value that, once set, all miners will use.
	blockTxPoolTABs, ok := txPoolBlockTABs[parent.i+1]
	if !ok {
		blockTxPoolTABs = int64(normalDist.Rand())
		txPoolBlockTABs[parent.i+1] = blockTxPoolTABs
	}
	blockTAB := blockTxPoolTABs + m.Balance
	tabChange := int64(0)
	if blockTAB > parent.tabs {
		tabChange = 1
	} else if blockTAB < parent.tabs {
		tabChange = -1
	}

	tabFalls := parent.tabsFallCount
	if tabChange < 0 {
		tabFalls++
	} else {
		tabFalls = 0
	}

	// A naive model of uncle citations: block has uncles if any orphan blocks exist in our miner's record of the parent height
	uncles := len(m.Blocks[parent.i-1]) > 1
	blockDifficulty := getBlockDifficulty(parent /* interval: */, uncles, s-parent.s)

	tabs := getTABS(parent.tabs, blockTAB)
	if m.ConsensusAlgorithm == TDTABS_step {
		tabs = getTABS_step(parent.tabs, tabFalls, blockTAB)
	}

	tdtabs := tabs * blockDifficulty
	b := &Block{
		i:             parent.i + 1,
		s:             s, // miners are always honest about their timestamps
		si:            s - parent.s,
		d:             blockDifficulty,
		td:            parent.td + blockDifficulty,
		tabsFallCount: tabFalls,
		tabsCmp:       tabChange,
		tabs:          tabs,
		ttdtabs:       parent.ttdtabs + tdtabs,
		miner:         m.Address,
		ph:            parent.h,
		h:             fmt.Sprintf("%08x", rand.Int63()),
	}
	m.processBlock(b)
	m.broadcastBlock(b)
}

func (m *Miner) broadcastBlock(b *Block) {
	b.delay = Delay{
		withhold: m.SendDelay(b),
		material: m.Latency(),
	}
	for _, n := range m.neighbors {
		n.receiveBlock(b)
	}
}

func (m *Miner) receiveBlock(b *Block) {
	if m.ReceiveDelay != nil {
		b.delay.postpone = m.ReceiveDelay(b)
	}
	if d := b.delay.Total(); d > 0 {
		if len(m.receivedBlocks[b.s+d]) > 0 {
			m.receivedBlocks[b.s+d] = append(m.receivedBlocks[b.s+d], b)
		} else {
			m.receivedBlocks[b.s+d] = Blocks{b}
		}
		return
	}
	m.processBlock(b)
}

func (m *Miner) processBlock(b *Block) {
	dupe := m.Blocks.AppendBlockByNumber(b)
	if !dupe {
		defer m.broadcastBlock(b)
	}

	// Special case: init genesis block.
	if m.head == nil {
		m.head = b
		m.head.canonical = true
		return
	}

	canon := m.arbitrateBlocks(m.head, b)
	canon.canonical = true
	m.setHead(canon)
}

// arbitrateBlocks selects one canonical block from any two blocks.
// It assumes that 'a' block is the incumbent, and that 'b' is later proposed;
// which is to say that the order is expected to be the availability order for the miner.
func (m *Miner) arbitrateBlocks(a, b *Block) *Block {
	// dedupe
	if a.h == b.h {
		return a
	}

	m.ConsensusArbitrations++          // its what we do here
	m.ConsensusObjectiveArbitrations++ // an assumption that will be undone (--) if it does not hold

	decisionCondition := "consensus_score_high"
	defer func() {
		m.decisionConditionTallies[decisionCondition]++
	}()

	if m.ConsensusAlgorithm == TD {
		// TD arbitration
		if a.td > b.td {
			return a
		} else if b.td > a.td {
			return b
		}
	} else if m.ConsensusAlgorithm == TDTABS || m.ConsensusAlgorithm == TDTABS_step {
		if (a.ttdtabs) > (b.ttdtabs) {
			return a
		} else if (b.ttdtabs) > (a.ttdtabs) {
			return b
		}
	}

	// Number arbitration
	decisionCondition = "height_low"
	if a.i < b.i {
		return a
	} else if b.i < a.i {
		return b
	}

	// If we've reached this point, the arbitration was not
	// objective.
	m.ConsensusObjectiveArbitrations--

	// Self-interest arbitration
	decisionCondition = "miner_selfish"
	if a.miner == m.Address && b.miner != m.Address {
		return a
	} else if b.miner == m.Address && a.miner != m.Address {
		return b
	}

	// Coin toss
	if m.StrategySkipRandom {
		decisionCondition = "first_seen"
		return a
	}
	decisionCondition = "random"
	if rand.Float64() < 0.5 {
		return a
	}
	return b
}

func (m *Miner) balanceAdd(i int64) {
	m.Balance += i
	if m.BalanceCap != 0 && m.Balance > m.BalanceCap {
		m.Balance = m.BalanceCap
	}
}

func (m *Miner) setHead(head *Block) {

	add, drop := 1, 0 // These will only be recorded if reorg is true. Otherwise noops.

	addCanon := func(b *Block) {
		b.canonical = true
		if b.miner == m.Address {
			m.balanceAdd(blockReward)
		}
		add++
	}

	dropCanon := func(b *Block) {
		if !b.canonical {
			return
		}
		if b.miner == m.Address {
			m.balanceAdd(-blockReward)
		}
		b.canonical = false
		drop++
	}

	doReorg := m.head.h != head.ph
	if doReorg {

		// No block above the new head will be canonical.
		for i := head.i + 1; ; i++ {
			if len(m.Blocks[i]) == 0 {
				// When reaching a height with no (zero) blocks,
				// assume there are no greater heights with blocks either.
				break
			}
			for _, b := range m.Blocks[i] {
				dropCanon(b)
			}
		}

		// All blocks at head height which are not the new head are not canonical.
		for _, b := range m.Blocks[head.i] {
			if b.h != head.h {
				dropCanon(b)
			}
		}

		// Iterate backwards from the parent of the head block
		// breaking when we find a common ancestor.
		for p := m.Blocks.GetParent(head); p != nil && !p.canonical; p = m.Blocks.GetParent(p) {
			for _, v := range m.Blocks[p.i] {
				dropCanon(v) // drop all from canon
			}
			addCanon(p) // add the one parent to canon
		}

		m.reorgs[head.i] = reorg{add, drop}

		// fmt.Println("Reorg!", m.Address, head.i, "add", add, "drop", drop)
	}

	m.head = head
	headI := head.i

	addCanon(m.head)

	m.cord <- minerEvent{
		minerI: int(m.Index),
		i:      headI,
		blocks: m.Blocks[headI],
	}
}

type reorg struct {
	add, drop int
}

func (r reorg) magnitude() float64 {
	return float64(r.add + r.drop)
}

func (m *Miner) reorgMagnitudes() (magnitudes []float64) {
	for _, v := range m.reorgs {
		magnitudes = append(magnitudes, v.magnitude())
	}
	return
}

type ConsensusAlgorithm int

const (
	None ConsensusAlgorithm = iota
	TD
	TDTABS
	TDTABS_step // sequence-derived step algorithm for tabs numerator
	TimeDesc    // FreshnessPreferred
)

func (c ConsensusAlgorithm) String() string {
	switch c {
	case TD:
		return "TD"
	case TDTABS:
		return "TDTABS"
	case TDTABS_step:
		return "TDTABS_step"
	// case TimeAsc:
	// 	return "TimeAsc"
	case TimeDesc:
		return "TimeDesc"
	}
	panic("impossible")
}

type Block struct {
	i             int64  // H_i: number
	s             int64  // H_s: timestamp
	si            int64  // interval
	d             int64  // H_d: difficulty
	td            int64  // H_td: total difficulty
	tabsFallCount int64  // scalar value tracking how many blocks in sequence have had falling TABS scores
	tabsCmp       int64  // +/- TABS vs parent. Shortcut used for helping malicious miners figure out if they can try to beat a received block by postponing.
	tabs          int64  // H_k: TAB synthesis
	ttdtabs       int64  // H_k: TTABSConsensusScore, aka Total TD*TABS
	miner         string // H_c: coinbase/etherbase/author/beneficiary
	h             string // H_h: hash
	ph            string // H_p: parent hash
	canonical     bool

	delay Delay
}

type Delay struct {
	withhold int64 // selfishly withhold. This is controlled by the mining miner.
	postpone int64 // postpone processing to give self more time to mine last block. Controlled by the receiving miner.
	material int64 // ohms
}

func (d Delay) Total() int64 {
	return d.withhold + d.postpone + d.material
}

type Blocks []*Block
type BlockTree map[int64]Blocks

func (bs Blocks) Len() int {
	return len(bs)
}

func NewBlockTree() BlockTree {
	return BlockTree(make(map[int64]Blocks))
}

func (bt BlockTree) String() string {
	out := ""
	for i := int64(0); i < int64(len(bt)); i++ {

		out += fmt.Sprintf("n=%d ", i)
		for _, b := range bt[i] {
			out += b.String()
		}
		out += "\n"
	}

	return out
}

func (b *Block) String() string {
	return fmt.Sprintf("[i=%d s=%v(+%d) h=%s ph=%s d=%v td=%v c=%v]", b.i, b.s, b.si, b.h[:4], b.ph[:4], b.d, b.td, b.canonical)
}

func (bt BlockTree) AppendBlockByNumber(b *Block) (dupe bool) {
	if _, ok := bt[b.i]; !ok {
		// Is new block for number i
		bt[b.i] = Blocks{b}
		return false
	} else {
		// Is competitor block for number i

		for _, bb := range bt[b.i] {
			if b.h == bb.h {
				dupe = true
			}
		}
		if !dupe {
			bt[b.i] = append(bt[b.i], b)
		}
	}
	return dupe
}

// Ks returns a slice of K tallies (number of available blocks) for each block number.
// It weirdly returns a float64 because it will be used with stats packages
// that like []float64.
func (bt BlockTree) Ks() (ks []float64) {
	for _, v := range bt {
		if len(v) == 0 {
			panic("how?")
		}
		ks = append(ks, float64(len(v)))
	}
	return ks
}

// Intervals returns ALL block intervals for a tree (whether canonical or not).
// Again, []float64 is used because its convenient in context.
func (bt BlockTree) CanonicalIntervals() (intervals []float64) {
	for _, v := range bt {
		for _, b := range v {
			if b.canonical {
				intervals = append(intervals, float64(b.si))
			}
		}
	}
	return intervals
}

func (bt BlockTree) CanonicalDifficulties() (difficulties []float64) {
	for _, v := range bt {
		for _, b := range v {
			if !b.canonical {
				continue
			}
			difficulties = append(difficulties, float64(b.d))
		}
	}
	return difficulties
}

func (bt BlockTree) GetBlockByNumber(i int64) *Block {
	for _, bl := range bt[i] {
		if bl.canonical {
			return bl
		}
	}
	return nil
}

func (bt BlockTree) GetSideBlocksByNumber(i int64) (sideBlocks Blocks) {
	for _, bl := range bt[i] {
		if !bl.canonical {
			sideBlocks = append(sideBlocks, bl)
		}
	}
	return sideBlocks
}

func (bt BlockTree) GetBlockByHash(h string) *Block {
	for i := int64(len(bt) - 1); i >= 0; i-- {
		for _, b := range bt[i] {
			if b.h == h {
				return b
			}
		}
	}
	return nil
}

func (bt BlockTree) Where(condition func(*Block) bool) (blocks Blocks) {
	for _, v := range bt {
		for _, bl := range v {
			if !condition(bl) {
				continue
			}
			blocks = append(blocks, bl)
		}
	}
	return blocks
}

func (bt BlockTree) GetParent(b *Block) (parent *Block) {
	for _, v := range bt[b.i-1] {
		if v.h == b.ph {
			return v
		}
	}
	return nil
}

type minerResults struct {
	ConsensusAlgorithm ConsensusAlgorithm
	HashrateRel        float64
	HeadI              int64
	HeadTABS           int64

	KMean                      float64
	IntervalsMeanSeconds       float64
	DifficultiesRelGenesisMean float64

	Balance                 int64
	DecisiveArbitrationRate float64
	ReorgMagnitudesMean     float64
}

func ParseHexColor(s string) (c color.RGBA, err error) {
	c.A = 0xff
	switch len(s) {
	case 7:
		_, err = fmt.Sscanf(s, "#%02x%02x%02x", &c.R, &c.G, &c.B)
	case 4:
		_, err = fmt.Sscanf(s, "#%1x%1x%1x", &c.R, &c.G, &c.B)
		// Double the hex digits:
		c.R *= 17
		c.G *= 17
		c.B *= 17
	default:
		err = fmt.Errorf("invalid length, must be 7 or 4")

	}
	return
}

type HashrateDistType int

const (
	HashrateDistEqual HashrateDistType = iota
	HashrateDistLongtail
)

func (t HashrateDistType) String() string {
	switch t {
	case HashrateDistEqual:
		return "equal"
	case HashrateDistLongtail:
		return "longtail"
	default:
		panic("unknown")
	}
}

func generateMinerHashrates(ty HashrateDistType, n int) []float64 {
	if n < 1 {
		panic("must have at least one miner")
	}
	if n == 1 {
		return []float64{1}
	}

	out := []float64{}

	switch ty {
	case HashrateDistLongtail:
		rem := float64(1)
		for i := 0; i < n; i++ {
			var take float64
			var share float64
			if i == 0 {
				share = float64(1) / 3
			} else {
				share = 0.6
			}
			if i != n-1 {
				take = rem * share
			}
			if take > float64(1)/3*rem {
				take = float64(1) / 3 * rem
			}
			if i == n-1 {
				take = rem
			}
			out = append(out, take)
			rem = rem - take
		}
		sort.Slice(out, func(i, j int) bool {
			return out[i] > out[j]
		})
		return out
	case HashrateDistEqual:
		for i := 0; i < n; i++ {
			out = append(out, float64(1)/float64(n))
		}
		return out
	default:
		panic("impossible")
	}
}
