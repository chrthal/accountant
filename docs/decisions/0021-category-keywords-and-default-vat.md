# ADR 0021: Category keywords and default VAT rates

**Date:** 2026-10-15
**Status:** Accepted. Extends ADR 0020. "Every rate is 0 % under the Kleinunternehmerregelung" is superseded for expenses by ADR 0022.

## Context
In testing the bank drafts (ADR 0020), the user asked for two things:
- **check the available categories and assign them automatically**;
- **for outgoing payments, apply the default VAT rate** instead of the
  model's guess.

The model picked plausible categories, but only for lines it was asked about,
and only with a running Ollama. Its VAT rate for an expense is a guess about a
document it has never seen. The bank line does not state the rate.

## Decision

### Categories carry keywords and an optional default VAT rate
`categories.keywords` (comma-separated, stored lower-case and de-duplicated)
and `categories.default_vat_rate` (0/10/13/20 or none), both in migration
0011 and editable on the category form.

### The category rule (`match.CategoryFor`)
The bank line's counterparty and text are folded (lower case, umlauts as
ae/oe/ue, punctuation as spaces) and searched:
1. **Keywords:** a keyword matches at a word start ("telefon" finds
   "Telefonrechnung"). Keywords of three letters or fewer must be whole words,
   so "a1" does not fire on a customer number "A12345".
2. **Category names**, only when no keyword matched anywhere: name words of
   four letters or more, also at word starts ("Miete" in "Miete September").

A line that hits **two categories gets none**. A tie is a guess, and the line
is left for the user or the model.

### Order of precedence in a draft
1. An earlier booking for the same counterparty ("wie zuletzt", ADR 0020).
2. The category rule ("Kategorie erkannt", with the keyword in the tooltip),
   preselected.
3. The model, which is now only asked when neither applies.

### Where the VAT rate comes from
For an **outgoing** payment, in this order:
1. the earlier booking;
2. the category's default rate;
3. the new setting **"Standard-USt-Satz für Ausgaben"** (default 20 %).

**The model's VAT rate is never used for an expense.** Income keeps the
model's rate, marked "prüfen". Under the Kleinunternehmerregelung every rate
is 0 %. The draft shows where the rate came from ("Kategorie", "Standard",
"prüfen"). The manual booking form starts at the standard rate for expenses,
and choosing a category with a default rate sets the field (`app.js`). The
server prefills the same values itself, so this also works without script.

### A missing form field is not 0 %
The settings form saves all fields at once. A request without
`default_vat_outgoing` keeps 20 % rather than storing 0, which would quietly
zero the Vorsteuer on every new expense draft.

## Consequences
- The user maintains the keywords, which are short and their own. Common
  supermarkets, telecom providers and software vendors cover most of a small
  business's bank lines without any model.
- The category rule runs for every open line on every page view. It is
  string matching over a handful of categories, so the cost does not matter.

## Alternatives considered
- **Letting the model decide categories everywhere:** it needs a running
  Ollama, is slower, and is less explainable than "Stichwort »billa«".
- **Regular expressions as keywords:** more powerful, but easy to get wrong
  for a non-developer. Word-start matching covers the real cases.
- **Longest keyword wins when two categories match:** this decides an
  ambiguous line by an accident of spelling.
