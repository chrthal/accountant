# 0045 — A booking's business share can be changed

## Context
ADR 0041 stored the business share on the booking and left a wrong share
to a Storno and a new booking. That breaks the bank match and the Beleg
link, and adds two bookings for one number. The user asked to edit the
share on the booking. Unlike the description (ADR 0043), the share changes
figures: the cost for the profit and the deductible Vorsteuer.

## Decision
- **The same mechanism as the description:** the booking row stays as
  written (ADR 0007); a change is a new row in `entry_shares` (share, note,
  time), append-only with its own triggers. The newest row counts
  (`currentPct`, `currentNote`) in every figure: totals, VAT periods,
  category breakdown, exports, and the booking itself.
- **The booking page** has a "Betrieblicher Anteil" card with the history
  ("gebucht mit", then each change) and a form. It says the change affects
  the booking's period, and to ask the Steuerberater whether a VAT return
  already filed is corrected.
- **Not for:** income (no private share), a Storno pair (counts nowhere),
  and an asset, whose cost was fixed from the share when it was created
  (ADR 0037); there the asset is removed, the share changed, the asset
  created again.
- A Storno takes the share in force at that moment, so the pair cancels.
- The new table is in the backups.

## Consequences
- The figures of a past period change without a booking in the current
  one. That is the same effect as a Storno and a new booking on the old
  date, which also leaves the old period changed. Whether a filed UVA is
  corrected in its own period or in the next is for the Steuerberater;
  the page says so, and the history shows what changed when.

## Alternatives considered
- **Storno and rebook automatically, moving the bank match and the
  Belege:** the same figures with two more bookings and more to get wrong.
- **Allow changes only in the current period:** would force a Storno for
  an honest late correction, which changes the old period anyway.
