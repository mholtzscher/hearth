package dashboards

import (
	"fmt"
	"slices"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func newComposeProps(kind string) composeProps {
	switch kind {
	case "Stack":
		return &stackProps{}
	case "Grid":
		return &gridProps{}
	case "Heading", "Note":
		return &textProps{}
	case "Metric":
		return &metricProps{}
	case timeSeriesNode:
		return &timeSeriesProps{}
	case "State", "WeekPattern":
		return &readingRow{}
	case "ReadingList":
		return &readingListProps{}
	case "Device":
		return &deviceProps{}
	case stateTimelineNode:
		return &stateTimelineProps{}
	case "ToggleTile":
		return &toggleTileProps{}
	case toggleListNode:
		return &toggleListProps{}
	case "OutsideNow":
		return &outsideNowProps{}
	case "Rain":
		return &rainProps{}
	case "Wind":
		return &windProps{}
	case "Pressure":
		return &pressureProps{}
	case "Forecast":
		return &forecastProps{}
	case "AirPurifier":
		return &airPurifierProps{}
	case "Cover":
		return &coverProps{}
	case "Thermostat":
		return &thermostatProps{}
	case "MediaPlayer":
		return &mediaPlayerProps{}
	default:
		return nil
	}
}

// cardProps holds the portable properties assembled from the typed authoring
// inputs. The portable schema and presenter remain the final admission gate.
type cardProps map[string]any

func (p cardProps) optionalText(key, value string) {
	if value != "" {
		p[key] = value
	}
}

func labeledProps(label string) cardProps {
	p := cardProps{}
	p.optionalText("label", label)
	return p
}

type composeSource struct {
	EntityID devices.EntityID `json:"entity_id"`
	Field    string           `json:"field"`
}

type composedSource struct {
	Kind       string               `json:"kind"`
	EntityID   devices.EntityID     `json:"entity_id"`
	EntityType devices.EntityTypeID `json:"entity_type"`
	Field      string               `json:"field,omitempty"`
	Window     string               `json:"window,omitempty"`
}

// A card captures its first failing source and stops resolving further ones.
// All paths are made from fixed field names and numeric positions.
type cardCompiler struct {
	view *viewCompiler
	path string
	err  error
}

func (c *cardCompiler) scalar(source composeSource, path string) composedSource {
	state := c.root(source.EntityID, path+"/entity_id")
	if c.err == nil {
		if _, found := devices.BuiltinStateField(state.EntityType, source.Field); !found {
			c.err = invalidViewArgument(
				c.path+path+"/field",
				"choose a State field of the selected Entity; omit field for a scalar root",
			)
		}
	}
	state.Field = source.Field
	return state
}

func (c *cardCompiler) root(id devices.EntityID, path string, types ...devices.EntityTypeID) composedSource {
	if c.err != nil {
		return composedSource{}
	}
	entityType, err := c.view.entityType(id, c.path+path)
	if err != nil {
		c.err = err
	} else if len(types) > 0 && !slices.Contains(types, entityType) {
		c.err = invalidViewArgument(c.path+path, "the selected Entity type is not compatible with this card or role")
	}
	return composedSource{Kind: "entity-state", EntityID: id, EntityType: entityType}
}

func (c *cardCompiler) optionalRoot(p cardProps, key string, id devices.EntityID, entityType devices.EntityTypeID) {
	if id != "" {
		p[key] = c.root(id, "/"+key, entityType)
	}
}

type textProps struct {
	Text string `json:"text"`
}

func (p *textProps) compile(_ *cardCompiler) cardProps { return cardProps{"text": p.Text} }

type metricProps struct {
	Source composeSource `json:"source"`
	Label  string        `json:"label"`
	Trend  string        `json:"trend"`
	Range  bool          `json:"range"`
}

func (p *metricProps) compile(c *cardCompiler) cardProps {
	props := labeledProps(p.Label)
	props["source"] = c.scalar(p.Source, "/source")
	props.optionalText("trend", p.Trend)
	if p.Range {
		props["range"] = true
	}
	return props
}

type labeledEntity struct {
	EntityID devices.EntityID `json:"entity_id"`
	Label    string           `json:"label"`
}

func (p labeledEntity) compileRoot(c *cardCompiler, types ...devices.EntityTypeID) cardProps {
	props := labeledProps(p.Label)
	props["source"] = c.root(p.EntityID, "/entity_id", types...)
	return props
}

type timeSeriesProps struct {
	Title  string          `json:"title"`
	Series []labeledEntity `json:"series"`
	Field  string          `json:"field"`
	Window string          `json:"window"`
}

func (p *timeSeriesProps) compile(c *cardCompiler) cardProps {
	series := make([]cardProps, 0, len(p.Series))
	seen := make(map[devices.EntityID]bool, len(p.Series))
	var chartType devices.EntityTypeID
	for i, item := range p.Series {
		path := fmt.Sprintf("/series/%d/entity_id", i)
		source := c.root(item.EntityID, path)
		if c.err != nil {
			break
		}
		if seen[item.EntityID] || (i != 0 && chartType != source.EntityType) {
			c.err = invalidViewArgument(c.path+path, "a TimeSeries needs distinct Entities of one type")
			break
		}
		if _, found := devices.BuiltinStateField(source.EntityType, p.Field); !found {
			c.err = invalidViewArgument(c.path+"/field", "choose a State field shared by the chart's Entities")
			break
		}
		chartType, seen[item.EntityID] = source.EntityType, true
		source.Kind, source.Field, source.Window = seriesBindingKind, p.Field, p.Window
		props := labeledProps(item.Label)
		props["source"] = source
		series = append(series, props)
	}
	return cardProps{viewTitleProperty: p.Title, "series": series}
}

type booleanWords struct {
	True  string `json:"true"`
	False string `json:"false"`
}

type readingRow struct {
	Source composeSource `json:"source"`
	Label  string        `json:"label"`
	Words  *booleanWords `json:"words"`
}

func (p *readingRow) compile(c *cardCompiler) cardProps {
	props := labeledProps(p.Label)
	props["source"] = c.scalar(p.Source, "/source")
	if p.Words != nil {
		props["words"] = p.Words
	}
	return props
}

type readingListProps struct {
	Title string       `json:"title"`
	Rows  []readingRow `json:"rows"`
}

func (p *readingListProps) compile(c *cardCompiler) cardProps {
	rows := make([]cardProps, 0, len(p.Rows))
	for i := range p.Rows {
		row := cardCompiler{view: c.view, path: fmt.Sprintf("%s/rows/%d", c.path, i)}
		rows = append(rows, p.Rows[i].compile(&row))
		if row.err != nil {
			c.err = row.err
			break
		}
	}
	return cardProps{viewTitleProperty: p.Title, "rows": rows}
}

type stateTimelineProps struct {
	readingListProps

	Style  string `json:"style"`
	Window string `json:"window"`
}

func (p *stateTimelineProps) compile(c *cardCompiler) cardProps {
	props := p.readingListProps.compile(c)
	props["style"], props["window"] = p.Style, p.Window
	return props
}

type deviceProps struct {
	DeviceID devices.DeviceID `json:"device_id"`
	Label    string           `json:"label"`
}

func (p *deviceProps) compile(c *cardCompiler) cardProps {
	path := c.path + "/device_id"
	if _, err := devices.ParseDeviceID(string(p.DeviceID)); err != nil {
		c.err = invalidViewArgument(path, "device_id must be a canonical Device ID")
	} else {
		c.err = c.view.recordResource(string(p.DeviceID), path)
	}
	props := labeledProps(p.Label)
	props["device"] = cardProps{"kind": deviceBindingKind, "device_id": p.DeviceID}
	return props
}

type meterProps struct {
	Source composeSource `json:"source"`
	Window string        `json:"window"`
	Detail *struct {
		Source composeSource `json:"source"`
		Label  string        `json:"label"`
	} `json:"detail"`
}

type toggleTileProps struct {
	labeledEntity

	Metric *meterProps `json:"metric"`
}

func (p *toggleTileProps) compile(c *cardCompiler) cardProps {
	props := p.compileRoot(c, devices.EntityTypePowerV1)
	if meter := p.Metric; meter != nil {
		metric := cardProps{"source": c.scalar(meter.Source, "/metric/source"), "window": meter.Window}
		if meter.Detail != nil {
			detail := labeledProps(meter.Detail.Label)
			detail["source"] = c.scalar(meter.Detail.Source, "/metric/detail/source")
			metric["detail"] = detail
		}
		props["metric"] = metric
	}
	return props
}

type toggleListProps struct {
	Title  string          `json:"title"`
	Rows   []labeledEntity `json:"rows"`
	AllOff bool            `json:"all_off"`
}

func (p *toggleListProps) compile(c *cardCompiler) cardProps {
	rows := make([]cardProps, 0, len(p.Rows))
	for i, row := range p.Rows {
		props := labeledProps(row.Label)
		props["source"] = c.root(row.EntityID, fmt.Sprintf("/rows/%d/entity_id", i), devices.EntityTypePowerV1)
		rows = append(rows, props)
	}
	props := cardProps{viewTitleProperty: p.Title, "rows": rows}
	if p.AllOff {
		props["all_off"] = true
	}
	return props
}

type outsideNowProps struct {
	Label         string           `json:"label"`
	Temperature   devices.EntityID `json:"temperature"`
	Humidity      devices.EntityID `json:"humidity"`
	Wind          devices.EntityID `json:"wind"`
	WindDirection devices.EntityID `json:"wind_direction"`
	RainRate      devices.EntityID `json:"rain_rate"`
	RainToday     devices.EntityID `json:"rain_today"`
}

func (p *outsideNowProps) compile(c *cardCompiler) cardProps {
	props := labeledProps(p.Label)
	props["temperature"] = c.root(p.Temperature, "/temperature", devices.EntityTypeTemperatureV1)
	c.optionalRoot(props, "humidity", p.Humidity, devices.EntityTypeRelativehumidityV1)
	c.optionalRoot(props, "wind", p.Wind, devices.EntityTypeSpeedV1)
	c.optionalRoot(props, "wind_direction", p.WindDirection, devices.EntityTypeNumericsensorV1)
	c.optionalRoot(props, "rain_rate", p.RainRate, devices.EntityTypeNumericsensorV1)
	c.optionalRoot(props, "rain_today", p.RainToday, devices.EntityTypeNumericsensorV1)
	return props
}

type rainProps struct {
	Label  string           `json:"label"`
	Rate   devices.EntityID `json:"rate"`
	Storm  devices.EntityID `json:"storm"`
	Totals *struct {
		Day   devices.EntityID `json:"day"`
		Week  devices.EntityID `json:"week"`
		Month devices.EntityID `json:"month"`
		Year  devices.EntityID `json:"year"`
	} `json:"totals"`
	Trend string `json:"trend"`
}

func (p *rainProps) compile(c *cardCompiler) cardProps {
	props := labeledProps(p.Label)
	props["rate"] = c.root(p.Rate, "/rate", devices.EntityTypeNumericsensorV1)
	c.optionalRoot(props, "storm", p.Storm, devices.EntityTypeNumericsensorV1)
	if totals := p.Totals; totals != nil && c.err == nil {
		totalProps := cardProps{}
		scope := cardCompiler{view: c.view, path: c.path + "/totals"}
		scope.optionalRoot(totalProps, "day", totals.Day, devices.EntityTypeNumericsensorV1)
		scope.optionalRoot(totalProps, "week", totals.Week, devices.EntityTypeNumericsensorV1)
		scope.optionalRoot(totalProps, "month", totals.Month, devices.EntityTypeNumericsensorV1)
		scope.optionalRoot(totalProps, "year", totals.Year, devices.EntityTypeNumericsensorV1)
		c.err = scope.err
		props["totals"] = totalProps
	}
	props.optionalText("trend", p.Trend)
	return props
}

type windProps struct {
	Label     string           `json:"label"`
	Speed     devices.EntityID `json:"speed"`
	Gust      devices.EntityID `json:"gust"`
	Direction devices.EntityID `json:"direction"`
	MaxGust   devices.EntityID `json:"max_gust"`
	Trend     string           `json:"trend"`
}

func (p *windProps) compile(c *cardCompiler) cardProps {
	props := labeledProps(p.Label)
	props["speed"] = c.root(p.Speed, "/speed", devices.EntityTypeSpeedV1)
	c.optionalRoot(props, "gust", p.Gust, devices.EntityTypeSpeedV1)
	c.optionalRoot(props, "direction", p.Direction, devices.EntityTypeNumericsensorV1)
	c.optionalRoot(props, "max_gust", p.MaxGust, devices.EntityTypeSpeedV1)
	props.optionalText("trend", p.Trend)
	return props
}

type pressureProps struct {
	labeledEntity

	Trend string `json:"trend"`
}

func (p *pressureProps) compile(c *cardCompiler) cardProps {
	props := p.compileRoot(c, devices.EntityTypePressureV1)
	props.optionalText("trend", p.Trend)
	return props
}

type forecastProps struct {
	labeledEntity

	Periods *int `json:"periods"`
}

func (p *forecastProps) compile(c *cardCompiler) cardProps {
	props := labeledProps(p.Label)
	source := c.root(p.EntityID, "/entity_id", devices.EntityTypeHourlyforecastV1, devices.EntityTypeDailyforecastV1)
	props["source"] = source
	if p.Periods != nil && c.err == nil {
		c.err = validateForecastPeriods(source.EntityType, *p.Periods, c.path+"/periods")
		props["periods"] = *p.Periods
	}
	return props
}

func validateForecastPeriods(entityType devices.EntityTypeID, periods int, path string) error {
	switch {
	case entityType == devices.EntityTypeHourlyforecastV1 && (periods < 6 || periods > 48):
		return invalidViewArgument(path, "hourly forecasts show 6 to 48 periods")
	case entityType == devices.EntityTypeDailyforecastV1 && (periods < 3 || periods > 8):
		return invalidViewArgument(path, "daily forecasts show 3 to 8 periods")
	}
	return nil
}

type airPurifierProps struct {
	Layout     string           `json:"layout"`
	Label      string           `json:"label"`
	Power      devices.EntityID `json:"entity_id"`
	PM25       devices.EntityID `json:"pm25"`
	Mode       devices.EntityID `json:"fan_mode"`
	FanLevel   devices.EntityID `json:"fan_level"`
	Display    devices.EntityID `json:"display"`
	ChildLock  devices.EntityID `json:"child_lock"`
	FilterLife devices.EntityID `json:"filter_life"`
}

func (p *airPurifierProps) compile(c *cardCompiler) cardProps {
	props := labeledProps(p.Label)
	props["layout"] = p.Layout
	props["power"] = c.root(p.Power, "/entity_id", devices.EntityTypePowerV1)
	props["pm25"] = c.root(p.PM25, "/pm25", devices.EntityTypeNumericsensorV1)
	if p.Mode != "" {
		props["mode"] = c.root(p.Mode, "/fan_mode", devices.EntityTypeEnumsettingV1)
	}
	c.optionalRoot(props, "fan_level", p.FanLevel, devices.EntityTypeEnumsettingV1)
	c.optionalRoot(props, "display", p.Display, devices.EntityTypePowerV1)
	c.optionalRoot(props, "child_lock", p.ChildLock, devices.EntityTypePowerV1)
	c.optionalRoot(props, "filter_life", p.FilterLife, devices.EntityTypeNumericsensorV1)
	return props
}

type coverProps struct {
	labeledEntity

	Layout      string `json:"layout"`
	ConfirmOpen *bool  `json:"confirm_open"`
}

func (p *coverProps) compile(c *cardCompiler) cardProps {
	props := p.compileRoot(c, devices.EntityTypeCoverV1)
	props["layout"] = p.Layout
	if p.ConfirmOpen != nil {
		props["confirm_open"] = *p.ConfirmOpen
	}
	return props
}

type thermostatProps struct{ labeledEntity }

func (p *thermostatProps) compile(c *cardCompiler) cardProps {
	return p.compileRoot(c, devices.EntityTypeThermostatV1)
}

type mediaPlayerProps struct {
	labeledEntity

	Layout string `json:"layout"`
}

func (p *mediaPlayerProps) compile(c *cardCompiler) cardProps {
	props := p.compileRoot(c, devices.EntityTypeMediaplayerV1)
	props.optionalText("layout", p.Layout)
	return props
}
