# 0034 — Bank data stays CSV-only for now

## Context
CLAUDE.md kept an open question since the MVP: CAMT.053 or CSV only (ADR
0009 left CAMT.053 as a stretch goal). The user asked how to connect an
Austrian bank without a third-party provider. The options, as of October
2026:

- **EBICS 3.0** is the direct channel between a business and its bank.
  Austria joined EBICS in 2020; since 7 October 2024 banks have moved their
  business customers from MBS to EBICS, and MBS was supported until the end
  of June 2026. Raiffeisen delivers camt.053, camt.052 and MT940 over it and
  takes pain.001. It needs an EBICS contract (for example €50 setup and
  €13 a month at VKB-Bank), generated keys and signed INI/HIA letters.
  Open-source clients exist in Python and Kotlin; there is no mature one in
  Go.
- **CAMT.053 files** can be downloaded from ELBA business by hand. They are
  structured XML (ISO 20022) and the same files EBICS delivers.
- **PSD2 / XS2A APIs** only serve licensed third-party providers: an FMA
  AISP licence and a qualified eIDAS certificate (QWAC), also for reading
  one's own account. Not possible for a self-hosted tool.
- **FinTS/HBCI** is a German standard that Austrian banks do not offer.

A meinELBA CSV export ("Umsätze → Suche") imports cleanly, with the labelled text split by the app (ADR 0020).

## Decision
The bank import stays the CSV import as built (ADR 0009, 0014, 0020). No
CAMT.053 parser and no EBICS client for now. The user chose this.

## Consequences
- Bank data comes in by a manual export from online banking, which works
  today.
- The open question is closed. It reopens when either of these is true:
  - a real CAMT.053 file is at hand: build the CAMT.053 import first, as
    its own parser feeding `store.ImportBankRows`;
  - the bank confirms EBICS for the account and its price: EBICS would
    fetch the same CAMT.053 files automatically, so it builds on that
    parser.
- Importing the same period as CSV and, later, as CAMT would not be
  recognised as duplicates: the two formats hash differently. Whoever
  builds the CAMT import has to handle that.

## Alternatives considered
- **CAMT.053 import now**: more reliable than CSV, and the base for EBICS;
  deferred until a real file exists to build and test against.
- **EBICS now**: fully automatic, but needs a bank contract, key handling and
  XML signing without a Go library. Deferred until the bank has confirmed
  access and price.
- **PSD2 API**: needs a licence, see above.
- **FinTS**: not available in Austria.
