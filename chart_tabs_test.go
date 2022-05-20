package main

import (
	"testing"

	"golang.org/x/exp/shiny/materialdesign/colornames"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg/draw"
)

func TestPlotTABSDescending(t *testing.T) {

	p := plot.New()
	p.Title.Text = "TABS Adjustment Algorithms: Constant Numerator vs. Consecutive-Falls Stepping Numerator"
	p.Legend.Top = true

	tabsAdjustmentDenominator = 4096

	data := plotter.XYs{}
	dataStep := plotter.XYs{}

	localTAB := int64(genesisBlockTABS / 2)

	tabs := genesisBlockTABS
	tabsStep := genesisBlockTABS

	sequentialFalls := int64(0)
	for i := int64(1); i <= 4*60*24; i++ {
		tabs = getTABS(tabs, localTAB)
		data = append(data, plotter.XY{X: float64(i), Y: float64(tabs)})

		if localTAB >= tabsStep {
			sequentialFalls = 0
		} else {
			sequentialFalls++
		}
		tabsStep = getTABS_step(tabsStep, sequentialFalls, localTAB)
		dataStep = append(dataStep, plotter.XY{X: float64(i), Y: float64(tabsStep)})
	}

	scatter, _ := plotter.NewScatter(data)
	scatter.Shape = draw.CircleGlyph{}
	scatter.Radius = 1
	scatter.Color = colornames.Red200
	p.Add(scatter)
	p.Legend.Add("TABS", scatter)

	scatterStep, _ := plotter.NewScatter(dataStep)
	scatterStep.Shape = draw.CircleGlyph{}
	scatterStep.Radius = 1
	scatterStep.Color = colornames.Blue200
	p.Add(scatterStep)
	p.Legend.Add("TABS_step", scatterStep)

	if err := p.Save(800, 600, "tabs_desc.png"); err != nil {
		t.Fatal(err)
	}
}

func TestPlotConsensusScore(t *testing.T) {
	p := plot.New()
	p.Title.Text = "TDTABS Adjustment Algorithm Experiment: 51% Stake Attack, < 50% Miner Attack"
	p.Legend.Top = true

	tabsAdjustmentDenominator = 4096

	data := plotter.XYs{}
	dataStep := plotter.XYs{}

	localTAB := int64(genesisBlockTABS * 3 / 2)

	tabs := genesisBlockTABS
	tabsStep := genesisBlockTABS

	sequentialFalls := int64(0)
	for i := int64(1); i <= 4*60*24; i++ {

		tabs = getTABS(tabs, localTAB)
		data = append(data, plotter.XY{X: float64(i), Y: float64(tabs)})

		if localTAB >= tabsStep {
			sequentialFalls = 0
		} else {
			sequentialFalls++
		}
		tabsStep = getTABS_step(tabsStep, sequentialFalls, localTAB)
		dataStep = append(dataStep, plotter.XY{X: float64(i), Y: float64(tabsStep)})

	}

	scatter, _ := plotter.NewScatter(data)
	scatter.Shape = draw.CircleGlyph{}
	scatter.Radius = 1
	scatter.Color = colornames.Red200
	p.Add(scatter, plotter.NewGrid())
	p.Legend.Add("TABS", scatter)

	scatterStep, _ := plotter.NewScatter(dataStep)
	scatterStep.Shape = draw.CircleGlyph{}
	scatterStep.Radius = 1
	scatterStep.Color = colornames.Blue200
	p.Add(scatterStep)
	p.Legend.Add("TABS_step", scatterStep)

	if err := p.Save(800, 600, "cs_experiment_1.png"); err != nil {
		t.Fatal(err)
	}
}
