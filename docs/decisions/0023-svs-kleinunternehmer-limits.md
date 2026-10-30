# ADR 0023: SVS Kleinunternehmer limits shown next to the VAT threshold

**Date:** 2026-10-15
**Status:** Accepted

## Context
The user asked for the SVS limit for Kleinunternehmer to be displayed as
well. Besides the VAT exemption (§ 6 Abs 1 Z 27 UStG), a small business can
apply to the SVS for an exemption from compulsory pension and health
insurance under the GSVG. It has two yearly limits, and the app already holds
the figures to watch them.

According to the SVS (svs.at, contentid 10007.846813, read 2026-10-15):
- Einkünfte limit: €6,221.28 (2024), €6,613.20 (2025, 2026);
- Umsatz limit: €35,000 (2020–2024), €55,000 (from 2025);
- accident insurance stays in force;
- the exemption starts at the earliest on 1 January of the year the
  application arrives, and depends on age and earlier insurance periods.

## Decision
- `report.SVSLimitsFor(year)` holds the published values per year. A year
  that is not on file shows "keine Werte" instead of carrying the last known
  limit forward, because the limits change every year.
- `report.ComputeSVS` compares two figures with them:
  - **Umsatz:** the same effective revenue as the VAT threshold;
  - **Einkünfte (Näherung):** `Totals.ProfitCents()`, which is income net of
    VAT minus the cost of expenses (gross in the exempt VAT period, ADR 0022).
- The Auswertung page always shows the card. The overview shows it when the
  new setting "SVS-Ausnahme für Kleinunternehmer beantragt" is on.

## What the app does not know, and says so
- **The Einkünfte the SVS uses come from the income-tax assessment**, after
  depreciation (AfA), the Gewinnfreibetrag and other items the books do not
  hold. The figure shown is an approximation, labelled as one.
- **Gross or net revenue:** the SVS page does not say. The VAT threshold's
  figure is used, and the card says so.
- **Consequences of an overshoot** are stated cautiously ("kann für dieses
  Jahr wegfallen, bitte mit der SVS klären"), not as a rule the app applies.
- Eligibility (age, earlier insurance periods, the application) is not checked.

## Consequences
- The limits need a yearly update in `internal/report/svs.go`, in the same
  commit as a pointer to the SVS source.

## Alternatives considered
- **Reuse the VAT threshold card:** the two exemptions have different limits,
  different authorities and different consequences.
- **Carry the last known limit forward:** silently wrong from the first
  January of every new year.
