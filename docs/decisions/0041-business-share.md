# 0041 — Business share of mixed-use expenses

## Context
Some expenses are used partly privately, such as a phone or internet
contract, or electricity in a home office. The user could only book them
in full. The deductible share of ADR 0037 does not fit: it lowers the
profit only and keeps the full Vorsteuer, which is right for Bewirtung but
wrong for private use.

The legal background (Austria, 2026; not tax advice):
- **Income tax:** only expenses caused by the business are Betriebsausgaben
  (§ 4 Abs 4 EStG). Costs of private life are not deductible (§ 20 Abs 1
  Z 1 and Z 2 lit a EStG). Mixed costs that can be split by an objective
  measure, such as the use of a phone, count by their business share. No
  law sets the share; the tax office accepts a reasonable estimate that is
  recorded, for example from an itemised bill.
- **VAT:** Vorsteuer only for supplies for the business (§ 12 Abs 1 UStG).
  A supply used less than 10 % for the business does not count as for the
  business (§ 12 Abs 2 Z 1 lit a UStG). For services such as a phone
  contract, the usual practice is to deduct the business share of the VAT.
- **Bewirtung differs:** § 12 Abs 2 Z 2 lit a UStG removes the Vorsteuer
  only where an expense is mostly (over 50 %) non-deductible under § 20
  EStG. A 50 % Bewirtung keeps its full Vorsteuer.

## Decision
- **Per booking** (`entries.business_pct`, 1–100, default 100, and
  `business_note` for how the share was found). Not per category: a share
  can change, and a change must not rewrite a VAT return already filed.
  This is the same rule as the stored VAT split (ADR 0007).
- **Profit:** the cost (ADR 0037) is net, or gross in the Kleinunternehmer
  time, × business share × the category's deductible share, rounded once.
- **Vorsteuer:** the business share of the VAT, rounded half up, and none
  under 10 % business use. The rest is shown as "USt auf Privatanteile",
  not deductible. In the Kleinunternehmer time all VAT stays blocked
  (ADR 0022), and it is not counted again as private.
- **Unchanged:** the gross amount, the cash result, bank matching and the
  invoice of the booking. The full amount was paid from the business
  account.
- **Asset:** only the business share of its cost is written off.
- **Storno** copies the share, so the pair cancels.
- **Shown:** form ("Teilweise privat genutzt?"), booking page, list badge,
  private share and private VAT on the report and VAT pages. The CSV has a
  column and a summary line for the private share. A bank line from the
  same counterparty as an earlier mixed booking starts from that share,
  in the form and in the bank drafts. The bank drafts and the review list
  of a bulk Beleg upload have the share and its note as fields in every
  row, defaulting to 100 %.
- **DATEV:** the row keeps the full amount, with "Betriebl. NN%:" in front
  of the Buchungstext. The VAT key comes from the account framework, and
  the private share needs the Steuerberater's accounts.

## Consequences
- **To confirm with the Steuerberater:**
  - the split of the Vorsteuer for mixed services, instead of a full
    deduction with private use taxed as Eigenverbrauch;
  - for goods (handset, laptop), the choice to assign the item fully to the
    business (EuGH C-269/00 "Seeling"), which the app does not model;
  - how the private share is booked in DATEV.
- A wrong share is changed on the booking, with its history kept
  (ADR 0045).
- The Arbeitsplatzpauschale (§ 4 Abs 4 Z 8 EStG) is a flat amount, not a
  share, and is not modelled.

## Alternatives considered
- **Share per category:** simpler to enter, but editing it would change
  earlier years and filed VAT returns.
- **Widen the deductible share of ADR 0037:** it does not touch the VAT,
  so the private part's Vorsteuer would still be deducted.
- **Book the private part as a separate private withdrawal:** two bookings
  for one bill, and they would not match the bank line.
