# 0037 — Deductible share (Bewirtung 50 %) and depreciation (AfA)

## Context
The app's profit was income net minus every expense in the year it was
paid. Two Austrian rules make that wrong:
- **Bewirtung** (business meals with an advertising purpose) is deductible
  only by half (§ 20 Abs 1 Z 3 EStG).
- An asset used for more than a year and costing more than €1,000 net (GWG
  limit, § 13 EStG) is written off over its useful life (**AfA**, § 7
  EStG), not in the year of purchase.
ADR 0027 left Bewirtung out of the default categories until the app could
handle it. The user asked for both.

## Decision
- **One idea, "cost for the profit"** (`costSQL`): an expense's net, or its
  gross where the VAT was not deductible (ADR 0022), times its category's
  **deductible share**, and nothing for an asset. Totals and the category
  breakdown use the same expression, so they cannot disagree.
- **Deductible share** per category (`deductible_pct`, 100 or 50 in the
  form). It changes the profit only. The VAT is unchanged: the input VAT on
  a 50 % Bewirtung is deducted in full.
- **Assets** classify one expense booking (`assets`): name, useful life,
  in-use date, and the cost, fixed when created (net, or gross where the VAT
  was not deductible). The booking stays as it is; its cash and VAT figures
  are untouched.
- **AfA** is linear (`report.Depreciation`): cost / years per year, from the
  year of first use. Used six months or less in that year (in use from
  1 July), half a year's amount there and the other half in an extra year
  at the end. Odd cents go to the last year, so the AfA always adds up to
  the cost. A reversed purchase depreciates nothing.
- **Visible**: a **Gewinn** block on Jahr & Export (income net − deductible
  expenses − AfA, plus the non-deductible part for information), an
  **Anlagenverzeichnis** page per year, an AfA row in the category table, the
  asset card on the booking with the GWG hint and typical useful lives, a
  note column and the Anlagenverzeichnis in the CSV export and the year
  archive. The SVS estimate (ADR 0023) uses the new profit.
- A new installation gets "Bewirtung (Geschäftsessen)" at 50 % among its
  default categories.

## Consequences
- The cash result ("Ergebnis", gross) is unchanged; the profit changes.
- Removing an asset puts its full cost back into the year it was paid, also
  in years already reported. The page warns about it.
- **Not tax advice; to confirm with the Steuerberater**:
  - the full input VAT on 50 % Bewirtung;
  - the half-year rule as modelled (first use from 1 July);
  - the GWG limit of €1,000 net;
  - the car rules (useful life at least 8 years, cost cap under § 8 Abs 6
    EStG): shown as a hint only, not enforced.
- **Not modelled**: degressive AfA, an asset leaving early (sale,
  withdrawal, loss: its book value would become an expense), the
  Gewinnfreibetrag, other non-deductible shares than 50 %.

## Alternatives considered
- **A Bewirtung flag per booking**: the category already carries defaults,
  and a booking cannot be edited (ADR 0007).
- **Depreciation as bookings**: AfA moves no money; bookings of it would
  show in cash, bank matching and VAT where it does not belong.
