package main

import (
	"fmt"
	"image/color"
	"io/ioutil"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/fogleman/gg"
	colorful "github.com/lucasb-eyer/go-colorful"
	"github.com/montanaflynn/stats"
	"golang.org/x/image/colornames"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"

	"github.com/mazznoer/colorgrad"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func TestPlotting(t *testing.T) {
	cases := []struct {
		name          string
		globalTweaks  func()
		minerMutation func(m *Miner)
	}{
		{
			name: "td",
			minerMutation: func(m *Miner) {
				m.ConsensusAlgorithm = TD
			},
		},

		// {
		// 	name: "td_skiprandom",
		// 	minerMutation: func(m *Miner) {
		// 		m.ConsensusAlgorithm = TD
		// 		m.StrategySkipRandom = true
		// 	},
		// },
		// {
		// 	name: "tdtabs_4096",
		// 	globalTweaks: func() {
		// 		tabsAdjustmentDenominator = 4096 // what Isaac considers "equilibrium", most conservative
		//
		// 	},
		// 	minerMutation: func(m *Miner) {
		// 		m.ConsensusAlgorithm = TDTABS
		// 	},
		// },
		// {
		// 	name: "tdtabs_4096_tabsStep",
		// 	globalTweaks: func() {
		// 		tabsAdjustmentDenominator = 4096 // what Isaac considers "equilibrium", most conservative
		//
		// 	},
		// 	minerMutation: func(m *Miner) {
		// 		m.ConsensusAlgorithm = TDTABS_step
		// 	},
		// },
		// {
		// 	name: "tdtabs_128",
		// 	globalTweaks: func() {
		// 		tabsAdjustmentDenominator = 128
		//
		// 	},
		// 	minerMutation: func(m *Miner) {
		// 		m.ConsensusAlgorithm = TDTABS
		// 	},
		// },
		// {
		// 	name: "tdtabs_64",
		// 	globalTweaks: func() {
		// 		tabsAdjustmentDenominator = 64 // aggressive
		//
		// 	},
		// 	minerMutation: func(m *Miner) {
		// 		m.ConsensusAlgorithm = TDTABS
		// 	},
		// },
		// {
		// 	name: "tdtabs_64_postpone_attack",
		// 	globalTweaks: func() {
		// 		tabsAdjustmentDenominator = 64
		// 	},
		// 	minerMutation: func(m *Miner) {
		// 		m.ConsensusAlgorithm = TDTABS
		//
		// 		// Evil.
		// 		//
		// 		m.ReceiveDelay = func(b *Block) int64 {
		// 			postpone := int64(receivePostponeSecondsDefault * float64(ticksPerSecond))
		// 			if m.ConsensusAlgorithm == TDTABS && m.Address != b.miner {
		// 				localTabs := m.Balance + txPoolBlockTABs[b.i]
		// 				if b.tabsCmp <= 0 && localTabs > b.tabs {
		// 					// The miner knows they have a better TABS than the received block.
		// 					// This gives them an edge in potential consensus points.
		//
		// 					// postpone = ticksPerSecond * (b.si % 9)
		// 					postpone += ticksPerSecond * 1 /* second */
		// 				}
		// 			}
		// 			return postpone
		// 		}
		// 	},
		// },
	}

	for _, c := range cases {
		c := c
		runTestPlotting(t, c.name, c.minerMutation)
	}

	// runTestPlotting(t, "td", func(m *Miner) {
	// 	m.ConsensusAlgorithm = TD
	// })
}

func minersNormal(minerEvents chan minerEvent, mut func(m *Miner)) (miners []*Miner) {

	hashrates := generateMinerHashrates(HashrateDistLongtail, int(countMiners))
	deriveMinerRelativeDifficultyHashes := func(genesisD int64, r float64) int64 {
		return int64(float64(genesisD) * r)
	}

	// We use relative hashrate as a proxy for balance;
	// more mining capital :: more currency capital.
	deriveMinerStartingBalance := func(genesisTABS int64, minerHashrate float64) int64 {
		// supply := genesisTABS * countMiners
		supply := genesisTABS / presumeMinerShareBalancePerBlockDenominator * countMiners
		return int64((float64(supply) * minerHashrate))
	}

	lastColor := colorful.Color{}
	grad := colorgrad.Viridis()

	for i := int64(0); i < countMiners; i++ {

		// set up their starting view of the chain
		bt := NewBlockTree()
		bt.AppendBlockByNumber(genesisBlock)

		// set up the miner

		// minerStartingBalance := deriveMinerStartingBalance(genesisBlock.tabs, hashrates[i])
		minerStartingBalance := deriveMinerStartingBalance(genesisBlock.tabs, hashrates[countMiners-1-i]) // backwards
		hashes := deriveMinerRelativeDifficultyHashes(genesisBlock.d, hashrates[i])

		clr := grad.At(1 - (hashrates[i] * (1 / hashrates[0])))
		if clr == lastColor {
			// Make sure colors (names) are unique.
			clr.R++
		}
		lastColor = clr
		minerName := clr.Hex()[1:]

		// format := "#%02x%02x%02x"
		// minerName := fmt.Sprintf("%02x%02x%02x", clr.R, clr.G, clr.B)

		m := &Miner{
			// ConsensusAlgorithm: TDTABS,
			// ConsensusAlgorithm: TD,
			Index:         i,
			Address:       minerName, // avoid collisions
			Hashrate:      hashrates[i],
			HashesPerTick: hashes,
			Balance:       minerStartingBalance,
			// BalanceCap:               minerStartingBalance,
			Blocks:                   bt,
			head:                     nil,
			receivedBlocks:           BlockTree{},
			neighbors:                []*Miner{},
			reorgs:                   make(map[int64]reorg),
			decisionConditionTallies: make(map[string]int),
			cord:                     minerEvents,
			SendDelay: func(block *Block) int64 {
				return int64(delaySecondsDefault * float64(ticksPerSecond))
				// return int64(hr * 3 * rand.Float64() * float64(ticksPerSecond))
			},
			Latency: func() int64 {
				return int64(latencySecondsDefault * float64(ticksPerSecond))
				// return int64(4 * float64(ticksPerSecond))
				// return int64((4 * rand.Float64()) * float64(ticksPerSecond))
			},
		}

		mut(m)

		m.processBlock(genesisBlock) // sets head to genesis
		miners = append(miners, m)
	}

	return miners
}

func minersTwo(minerEvents chan minerEvent, mut func(m *Miner)) (miners []*Miner) {

	// hashrates := generateMinerHashrates(HashrateDistLongtail, int(countMiners))
	hashrates := []float64{0.45, 0.35, 0.2}
	deriveMinerRelativeDifficultyHashes := func(genesisD int64, r float64) int64 {
		return int64(float64(genesisD) * r)
	}

	// We use relative hashrate as a proxy for balance;
	// more mining capital :: more currency capital.
	deriveMinerStartingBalance := func(genesisTABS int64, minerHashrate float64) int64 {
		// supply := genesisTABS * countMiners
		supply := genesisTABS / presumeMinerShareBalancePerBlockDenominator * countMiners
		return int64((float64(supply) * minerHashrate))
	}

	lastColor := colorful.Color{}
	grad := colorgrad.Viridis()

	for i := int64(0); i < countMiners; i++ {

		// set up their starting view of the chain
		bt := NewBlockTree()
		bt.AppendBlockByNumber(genesisBlock)

		// set up the miner

		// minerStartingBalance := deriveMinerStartingBalance(genesisBlock.tabs, hashrates[i])
		minerStartingBalance := deriveMinerStartingBalance(genesisBlock.tabs, hashrates[countMiners-1-i]) // backwards
		hashes := deriveMinerRelativeDifficultyHashes(genesisBlock.d, hashrates[i])

		clr := grad.At(1 - (hashrates[i] * (1 / hashrates[0])))
		if clr == lastColor {
			// Make sure colors (names) are unique.
			clr.R++
		}
		lastColor = clr
		minerName := clr.Hex()[1:]

		// format := "#%02x%02x%02x"
		// minerName := fmt.Sprintf("%02x%02x%02x", clr.R, clr.G, clr.B)

		m := &Miner{
			// ConsensusAlgorithm: TDTABS,
			// ConsensusAlgorithm: TD,
			Index:         i,
			Address:       minerName, // avoid collisions
			Hashrate:      hashrates[i],
			HashesPerTick: hashes,
			Balance:       minerStartingBalance,
			// BalanceCap:               minerStartingBalance,
			Blocks:                   bt,
			head:                     nil,
			receivedBlocks:           BlockTree{},
			neighbors:                []*Miner{},
			reorgs:                   make(map[int64]reorg),
			decisionConditionTallies: make(map[string]int),
			cord:                     minerEvents,
			SendDelay: func(block *Block) int64 {
				return int64(delaySecondsDefault * float64(ticksPerSecond))
				// return int64(hr * 3 * rand.Float64() * float64(ticksPerSecond))
			},
			Latency: func() int64 {
				return int64(latencySecondsDefault * float64(ticksPerSecond))
				// return int64(4 * float64(ticksPerSecond))
				// return int64((4 * rand.Float64()) * float64(ticksPerSecond))
			},
		}

		mut(m)

		m.processBlock(genesisBlock) // sets head to genesis
		miners = append(miners, m)
	}

	return miners
}

func runTestPlotting(t *testing.T, name string, mut func(m *Miner)) {

	t.Log("Running", name)

	outDir := filepath.Join("out", name)
	os.MkdirAll(outDir, os.ModePerm)
	os.RemoveAll(filepath.Join(outDir, "anim"))
	os.MkdirAll(filepath.Join(outDir, "anim"), os.ModePerm)

	miners := []*Miner{}
	minerEvents := make(chan minerEvent)
	blockRowsN := 150

	miners = minersNormal(minerEvents, mut)
	// miners = minersTwo(minerEvents, mut)

	// Create and install an attack miner.
	// This miner will NOT publish their blocks.
	// They will be rich.
	// attack: 1606651707293287461
	// defend:  203433894893418879
	attackerMinerBt := NewBlockTree()
	attackerMinerBt.AppendBlockByNumber(genesisBlock)
	attackMiner := &Miner{
		Index:         int64(len(miners)),
		Address:       "ff0000",
		Blocks:        attackerMinerBt,
		Hashrate:      0.9,
		HashesPerTick: int64(float64(genesisDifficulty) * 0.9),
		Balance:       genesisBlockTABS * 11 / 10, // rich enough to always win TABS
		BalanceCap:    0,
		CostPerBlock:  0,
		Latency: func() int64 {
			return int64(latencySecondsDefault * float64(ticksPerSecond))
		},
		SendDelay: func(block *Block) int64 {
			return int64(60 * 60 * 8 * float64(ticksPerSecond)) // 8 hour send delay
		},
		ReceiveDelay: func(block *Block) int64 {
			return int64(60 * 60 * 8 * float64(ticksPerSecond)) // 8 hour receive delay
		},
		ConsensusAlgorithm:             0,
		ConsensusArbitrations:          0,
		ConsensusObjectiveArbitrations: 0,
		StrategySkipRandom:             false,
		head:                           nil,
		receivedBlocks:                 BlockTree{},
		neighbors:                      []*Miner{},
		reorgs:                         make(map[int64]reorg),
		decisionConditionTallies:       make(map[string]int),
		cord:                           minerEvents,
	}
	mut(attackMiner)
	attackMiner.processBlock(genesisBlock)
	miners = append(miners, attackMiner)

	c := gg.NewContext(800, 1200)
	marginX, marginY := c.Width()/100, c.Width()/100

	c.Push()
	c.SetColor(colornames.White)
	c.DrawRectangle(0, 0, float64(c.Width()), float64(c.Height()))
	c.Fill()
	c.Stroke()
	c.Pop()

	c.SavePNG(filepath.Join(outDir, "anim", "out.png"))

	videoBlackRedForks := false
	// videoBlackRedForks := true

	go func() {
		c.Push() // unresolved state push

		for event := range minerEvents {
			// t.Log("minerEvent", event)

			// t.Log("here", 1)
			// t.Logf("event: %v", event)

			// ctx.DrawCircle(rand.Float64()*float64(c.Width), rand.Float64()*float64(c.Height), 10)

			xW := (c.Width() - (2 * marginX)) / int(countMiners)
			x := event.minerI*xW + marginX

			yH := (c.Height() - (2 * marginY)) / blockRowsN
			var y int64
			// if event.i > blockRowsN{
			// 	y = 0
			// } else {
			y = int64(c.Height()) - (event.i%int64(blockRowsN))*int64(yH) + int64(marginY)
			// }

			// Clear the row above on bottom-up overlap/overdraw.
			c.Push()
			c.SetColor(colornames.White)
			c.DrawRectangle(0, float64(y-int64(yH*5)), float64(c.Width()), float64(yH*5))
			c.Fill()
			c.Stroke()
			c.Pop()

			// if event.i > 200 {
			// 	// c.Push()
			// 	// c.Translate(0, float64(yH))
			// 	// c.Stroke()
			// 	// c.Pop()
			// }

			nblocks := len(event.blocks)

			// // Outline competitive blocks for visibility..
			// if nblocks > 1 {
			// 	c.Push()
			// 	c.SetColor(colornames.Black)
			// 	c.SetLineWidth(1)
			// 	c.DrawRectangle(float64(x-2), float64(y-2), float64(xW+4), float64(yH+4))
			// 	// c.Fill()
			// 	c.Stroke()
			// 	c.Pop()
			// }

			// Or, more better, when you're interested in seeing forks,
			// just don't print the uncontested blocks.
			// if nblocks <= 1 {
			// 	continue
			// }

			for ib, b := range event.blocks {
				// t.Logf("push")
				c.Push()
				if videoBlackRedForks {
					// Black blocks = uncontested
					// Red   blocks = network forks
					clr := colornames.Black
					if nblocks > 1 {
						clr = colornames.Red
					}
					c.SetColor(clr)
				} else {
					// Get the block color from the block's authoring miner.
					clr, err := ParseHexColor("#" + b.miner)
					if err != nil {
						t.Log("bad color", err.Error(), b.miner)
						panic("test")
					}
					c.SetColor(clr)
				}

				realX := float64(x)
				realX += float64(ib) * float64(xW/nblocks)

				rectMargin := float64(0)

				rectX, rectY := realX+rectMargin, float64(y)+rectMargin
				rectW, rectH := float64(xW/nblocks)-(2*rectMargin), float64(yH)-(2*rectMargin)

				// t.Log("here.ib", ib, b == nil, b.miner, clr)
				// t.Logf("x=%d y=%d width=%v height=%v b=%d/%d", x, y, xW, yH, ib, nblocks)
				c.DrawRectangle(rectX, rectY, rectW, rectH)
				c.Fill()
				c.Stroke()
				c.Pop()
				// t.Logf("pop")
			}

			// t.Log("here", 2)

		}
	}()

	for i, m := range miners {
		for j, mm := range miners {
			if i == j {
				continue
			}
			if rand.Float64() < minerNeighborRate {
				m.neighbors = append(m.neighbors, mm)
			}
		}
	}

	lastHighBlock := int64(0)
	for s := int64(1); s <= tickSamples; s++ {

		// for _, m := range miners {
		// 	m.doTick(s)
		// }

		// Randomize miner ticking.
		// This shouldn't do much, but should help a little smoothing any influence that
		// the arbitrary assignment ordering would have on block discovery outcomes.
		for _, i := range rand.Perm(len(miners)) {
			miners[i].doTick(s)
		}

		if s%ticksPerSecond == 0 {
			// time.Sleep(time.Millisecond * 100)
		}
		nextHighBlock := Miners(miners).headMax()
		if nextHighBlock > lastHighBlock {
			// if s%ticksPerSecond == 0 {

			if err := c.SavePNG(filepath.Join(outDir, "anim", fmt.Sprintf("%04d_f.png", nextHighBlock))); err != nil {
				t.Fatal("save png errored", err)
			}

			lastHighBlock = nextHighBlock
			// 	// Human-readable intervals.
			//
			// 	line := ""
			//
			// 	for i, m := range miners {
			// 		fmt.Sprintf(`%s`,
			// 			strings.Repeat("\", i))
			// 	}
		}

		// TODO: measure network graphs? eg. bifurcation tally?
	}

	t.Log("RESULTS", name)

	for i, m := range miners {
		kMean, _ := stats.Mean(m.Blocks.Ks())
		kMed, _ := stats.Median(m.Blocks.Ks())
		kMode, _ := stats.Mode(m.Blocks.Ks())

		intervalsMean, _ := stats.Mean(m.Blocks.CanonicalIntervals())
		intervalsMean = intervalsMean / float64(ticksPerSecond)
		difficultiesMean, _ := stats.Mean(m.Blocks.CanonicalDifficulties())

		reorgMagsMean, _ := stats.Mean(m.reorgMagnitudes())

		wins := m.Blocks.Where(func(b *Block) bool {
			return b.canonical && b.miner == m.Address
		}).Len()

		minerLog := fmt.Sprintf(`a=%s c=%s hr=%0.2f winr=%0.3f wins=%d head.i=%d head.tabs=%d head.td=%d head.tdtabs=%d k_mean=%0.3f k_med=%0.3f k_mode=%v intervals_mean=%0.3fs d_mean.rel=%0.3f balance=%d objective_decs=%0.3f arbs=%d reorgs.mag_mean=%0.3f
`,
			m.Address, m.ConsensusAlgorithm, m.Hashrate, float64(wins)/float64(m.head.i), wins, /* m.HashesPerTick, */
			m.head.i, m.head.tabs, m.head.td, m.head.ttdtabs,
			kMean, kMed, kMode,
			intervalsMean, difficultiesMean/float64(genesisBlock.d),
			m.Balance,
			float64(m.ConsensusObjectiveArbitrations)/float64(m.ConsensusArbitrations),
			m.ConsensusArbitrations,
			reorgMagsMean)

		// m.ConsensusArbitrations/m.head.i should be the kMean
		// This is: how many block decisions were arbitrated (ie how many total blocks were seen)
		// versus   how many blocks were canonical (how high the tree was).

		arbitrationConditionTallyLine := ""
		// I iterate these copypasta strings because I want order.
		for _, name := range []string{"consensus_score_high", "height_low", "miner_selfish", "random"} {
			v, ok := m.decisionConditionTallies[name]
			if !ok {
				continue
			}
			fv := float64(v) / float64(m.ConsensusArbitrations)
			arbitrationConditionTallyLine += fmt.Sprintf(`%s=%0.2f `, name, fv)
		}

		minerLog += arbitrationConditionTallyLine + "\n"

		t.Log(minerLog)

		// Log the stats of the miner
		ioutil.WriteFile(filepath.Join(outDir, fmt.Sprintf("miner_%d", i)), []byte(minerLog), os.ModePerm)
		// Log the block tree belonging to this miner
		ioutil.WriteFile(filepath.Join(outDir, fmt.Sprintf("miner_%d_bt", i)), []byte(m.Blocks.String()), os.ModePerm)
	}

	t.Log("Making plots...")

	plotIntervals := func() {
		filename := filepath.Join(outDir, "sample_intervals.png")
		p := plot.New()

		buckets := map[int]int{}
		for _, blocks := range miners[0].Blocks {
			for _, b := range blocks {
				buckets[int(b.si/ticksPerSecond)]++
			}
		}
		data := plotter.XYs{}
		for k, v := range buckets {
			data = append(data, plotter.XY{X: float64(k), Y: float64(v)})
		}
		hist, err := plotter.NewHistogram(data, len(buckets))
		if err != nil {
			panic(err)
		}
		p.Add(hist)
		p.Save(800, 300, filename)
	}
	plotIntervals()

	plotDifficulty := func() {
		filename := filepath.Join(outDir, "block_difficulties.png")
		p := plot.New()

		data := plotter.XYs{}
		for k, v := range miners[0].Blocks {
			data = append(data, plotter.XY{X: float64(k), Y: float64(v[0].d)})
		}
		scatter, err := plotter.NewScatter(data)
		if err != nil {
			panic(err)
		}
		scatter.Radius = 1
		scatter.Shape = draw.CircleGlyph{}
		p.Add(scatter)
		p.Y.Min = float64(genesisBlock.d) / 2 // low enough for sense of scale of variance
		p.Save(800, 300, filename)
	}
	plotDifficulty()

	plotTABS := func() {
		filename := filepath.Join(outDir, "block_tabs.png")
		p := plot.New()

		data := plotter.XYs{}
		for k, v := range miners[0].Blocks {
			data = append(data, plotter.XY{X: float64(k), Y: float64(v[0].tabs)})
		}
		scatter, err := plotter.NewScatter(data)
		if err != nil {
			panic(err)
		}
		scatter.Radius = 1
		scatter.Shape = draw.CircleGlyph{}
		p.Add(scatter)
		p.Y.Min = float64(genesisBlock.tabs) / 2 // low enough for sense of scale of variance
		p.Save(800, 300, filename)
	}
	plotTABS()

	plotMinerTDs := func() {
		filename := filepath.Join(outDir, "miner_tds.png")
		p := plot.New()

		for _, m := range miners {
			data := plotter.XYs{}
			for k, v := range m.Blocks {
				for _, b := range v {
					// Plot ALL blocks together.
					// Some numbers will be duplicated.
					if !b.canonical {
						continue
					}
					data = append(data, plotter.XY{X: float64(k), Y: float64(b.td)})
				}
			}

			scatter, err := plotter.NewScatter(data)
			if err != nil {
				panic(err)
			}
			scatter.Radius = 1
			scatter.Shape = draw.CircleGlyph{}
			scatter.Color, _ = ParseHexColor("#" + m.Address)
			p.Add(scatter)
			p.Legend.Add(m.Address, scatter)
		}

		// p.Y.Min = float64(genesisBlock.td)
		p.Save(800, 300, filename)
	}
	plotMinerTDs()

	plotMinerTDTABS := func() {
		filename := filepath.Join(outDir, "miner_ttdtabs_ts.png")
		p := plot.New()
		p.Title.Text = "Miner TD*TABS Values Over Timestamp"

		for _, m := range miners {
			data := plotter.XYs{}
			for _, v := range m.Blocks {
				for _, b := range v {
					// Plot ALL blocks together.
					// Some numbers will be duplicated.
					if !b.canonical {
						continue
					}
					// data = append(data, plotter.XY{X: float64(k), Y: float64(b.ttdtabs)})
					data = append(data, plotter.XY{X: float64(b.s), Y: float64(b.ttdtabs)})
				}
			}

			scatter, err := plotter.NewScatter(data)
			if err != nil {
				panic(err)
			}
			scatter.Radius = 1
			scatter.Shape = draw.CircleGlyph{}
			scatter.Color, _ = ParseHexColor("#" + m.Address)
			p.Add(scatter)
			p.Legend.Add(m.Address, scatter)
		}

		// p.Y.Min = float64(genesisBlock.td)
		p.Save(800, 300, filename)
	}
	plotMinerTDTABS()

	plotMinerTDTABSBlockN := func() {
		filename := filepath.Join(outDir, "miner_ttdtabs_blockn.png")
		p := plot.New()
		p.Title.Text = "Miner TD*TABS Values Over Block Height"

		for _, m := range miners {
			data := plotter.XYs{}
			for blockHeight, v := range m.Blocks {
				for _, b := range v {
					// Plot ALL blocks together.
					// Some numbers will be duplicated.
					if !b.canonical {
						continue
					}
					data = append(data, plotter.XY{X: float64(blockHeight), Y: float64(b.ttdtabs)})
					// data = append(data, plotter.XY{X: float64(b.s), Y: float64(b.ttdtabs)})
				}
			}

			scatter, err := plotter.NewScatter(data)
			if err != nil {
				panic(err)
			}
			scatter.Radius = 1
			scatter.Shape = draw.CircleGlyph{}
			scatter.Color, _ = ParseHexColor("#" + m.Address)
			p.Add(scatter)
			p.Legend.Add(m.Address, scatter)
		}

		// p.Y.Min = float64(genesisBlock.td)
		p.Save(800, 300, filename)
	}
	plotMinerTDTABSBlockN()

	plotMinerReorgs := func() {

		filename := filepath.Join(outDir, "miner_reorgs.png")
		p := plot.New()

		adds := plotter.XYs{}
		drops := plotter.XYs{}
		for i, m := range miners {
			i += 1
			centerMinerInterval := float64(i)
			for k, v := range m.reorgs {
				adds = append(adds, plotter.XY{X: float64(k), Y: float64(centerMinerInterval + float64(v.add)/20)})
				drops = append(drops, plotter.XY{X: float64(k), Y: float64(centerMinerInterval - float64(v.drop)/20)})
			}

			addScatter, err := plotter.NewScatter(adds)
			if err != nil {
				panic(err)
			}
			addScatter.Radius = 1
			addScatter.Shape = draw.CircleGlyph{}
			addScatter.Color = color.RGBA{R: 1, G: 255, B: 1, A: 255}
			p.Add(addScatter)

			dropScatter, err := plotter.NewScatter(drops)
			if err != nil {
				panic(err)
			}
			dropScatter.Radius = 1
			dropScatter.Shape = draw.CircleGlyph{}
			dropScatter.Color = color.RGBA{R: 255, G: 1, B: 1, A: 255}
			p.Add(dropScatter)
		}

		p.Y.Max = float64(len(miners) + 1)

		// p.Y.Min = float64(genesisBlock.td)
		p.Save(800, vg.Length(float64(len(miners)+1)*20), filename)
	}
	plotMinerReorgs()

	// plotMinerReorgMagnitudes := func() {
	// 	filename := filepath.Join("out", "miner_tds.png")
	// 	p := plot.New()
	//
	// 	data := plotter.XYs{}
	// 	for _, m := range miners {
	// 		for k, v := range m.re {
	// 			for _, b := range v {
	// 				// Plot ALL blocks together.
	// 				// Some numbers will be duplicated.
	// 				data = append(data, plotter.XY{X: float64(k), Y: float64(b.d)})
	// 			}
	// 		}
	//
	// 		scatter, err := plotter.NewScatter(data)
	// 		if err != nil {
	// 			panic(err)
	// 		}
	// 		scatter.Radius = 1
	// 		scatter.Shape = draw.CircleGlyph{}
	// 		scatter.Color, _ = ParseHexColor("#" + m.Address)
	// 		p.Add(scatter)
	// 		p.Legend.Add(m.Address, scatter)
	// 	}
	//
	// 	// p.Y.Min = float64(genesisBlock.td)
	// 	p.Save(800, 300, filename)
	// }
	// plotMinerReorgMagnitudes()

	/*
		https://superuser.com/questions/249101/how-can-i-combine-30-000-images-into-a-timelapse-movie

		ffmpeg -f image2 -r 1/5 -i img%03d.png -c:v libx264 -pix_fmt yuv420p out.mp4
		ffmpeg -f image2 -pattern_type glob -i 'time-lapse-files/*.JPG' …

	*/
	t.Log("Making movie...")
	movieCmd := exec.Command("/usr/bin/ffmpeg",
		"-f", "image2",
		"-r", "20/1", // 10 images / 1 second (Hz)
		// "-vframes", fmt.Sprintf("%d", lastHighBlock),
		"-pattern_type", "glob",
		"-i", filepath.Join(outDir, "anim", "*.png"),
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		filepath.Join(outDir, "anim", "out.mp4"),
	)
	if err := movieCmd.Run(); err != nil {
		t.Fatal(err)
	}

	/*
		https://askubuntu.com/questions/648603/how-to-create-an-animated-gif-from-mp4-video-via-command-line

		ffmpeg \
		  -i opengl-rotating-triangle.mp4 \
		  -r 15 \
		  -vf scale=512:-1 \
		  -ss 00:00:03 -to 00:00:06 \
		  opengl-rotating-triangle.gif
	*/
	t.Log("Making gif...")
	gifCmd := exec.Command("/usr/bin/ffmpeg",
		"-i", filepath.Join(outDir, "anim", "out.mp4"),
		// "-r", "10", // Hz value
		"-r", "20", // Hz value
		"-vf", "scale=512:-1",
		filepath.Join(outDir, "anim", "out.gif"),
	)
	if err := gifCmd.Run(); err != nil {
		t.Fatal(err)
	}

	animSlides, err := filepath.Glob(filepath.Join(outDir, "anim", "*.png"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range animSlides {
		// 			imgBaseName := fmt.Sprintf("%04d_f.png", nextHighBlock)
		base := filepath.Base(f)
		numStr := base[:4]
		if i, err := strconv.Atoi(numStr); err != nil && i%blockRowsN == 0 {
			continue
		} else if err != nil {
			t.Log(err)
		}
		os.Remove(f)
	}
}

func TestProcessBlock(t *testing.T) {
	m := &Miner{
		// ConsensusAlgorithm: TDTABS,
		// ConsensusAlgorithm: TD,
		Index:         0,
		Address:       "exampleMiner", // avoid collisions
		HashesPerTick: 42,
		Balance:       42000000,
		// BalanceCap:               minerStartingBalance,
		Blocks:                   NewBlockTree(),
		head:                     nil,
		receivedBlocks:           BlockTree{},
		neighbors:                []*Miner{},
		reorgs:                   make(map[int64]reorg),
		decisionConditionTallies: make(map[string]int),
		cord:                     make(chan minerEvent),
		SendDelay: func(*Block) int64 {
			return int64(delaySecondsDefault * float64(ticksPerSecond))
			// return int64(hr * 3 * rand.Float64() * float64(ticksPerSecond))
		},
		Latency: func() int64 {
			return int64(latencySecondsDefault * float64(ticksPerSecond))
			// return int64(4 * float64(ticksPerSecond))
			// return int64((4 * rand.Float64()) * float64(ticksPerSecond))
		},
	}

	// goroutine reads miner events chan (cord)
	go func() {
		for range m.cord {
		}
	}()

	m.processBlock(genesisBlock) // sets head to genesis

	ph := genesisBlock.h
	for i := int64(1); i < 10; i++ {
		b := &Block{i: i, canonical: true, ph: ph, h: fmt.Sprintf("%08x", rand.Int63())}
		ph = b.h
		m.Blocks.AppendBlockByNumber(b)
		m.setHead(b)
	}

	b := &Block{i: 8, canonical: true, ph: m.Blocks.GetBlockByNumber(7).h, h: fmt.Sprintf("%08x", rand.Int63())}
	m.Blocks.AppendBlockByNumber(b)
	m.setHead(b)

	b = &Block{i: 9, canonical: true, ph: b.h, h: fmt.Sprintf("%08x", rand.Int63())}
	m.Blocks.AppendBlockByNumber(b)
	m.setHead(b)

	b = &Block{i: 10, canonical: true, ph: b.h, h: fmt.Sprintf("%08x", rand.Int63())}
	m.Blocks.AppendBlockByNumber(b)
	m.setHead(b)

	t.Log(m.Blocks.String())
}
