package webui

import (
	"embed"
	"html/template"
)

//go:embed templates/*.html
var templateFS embed.FS

var templates = template.Must(template.New("ui").Funcs(template.FuncMap{
	// Formatting functions
	"date":            templateDate,
	"datetime":        templateDateTime,
	"ptime":           templatePTime,
	"money":           templateMoney,
	"strptr":          templateStrPtr,
	"json":            templateJSON,
	"cleanSessionURL": templateCleanSessionURL,

	// Phase-related functions
	"phaseProgress":    phaseProgress,
	"phaseFillClass":   phaseFillClass,
	"phaseStatusLabel": phaseStatusLabel,

	// Diff display functions
	"diffSummary": buildDiffSummary,
	"diffRows":    buildDiffRows,

	// Log display functions
	"logRows": buildLogRows,
}).ParseFS(templateFS, "templates/*.html"))
