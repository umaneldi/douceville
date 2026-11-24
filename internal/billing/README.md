# Paquet `billing`

Clients des prestataires de paiement utilisés pour **recharger le crédit
prépayé** des comptes. La logique de solde (débit, remboursement, journal) ne
se trouve pas ici mais dans `internal/store` (`billing.go`) et
`internal/api` (`billing_handlers.go`).

## Fichiers

| Fichier | Contenu |
| ------- | ------- |
| `provider.go` | Interface `PaymentProvider` (`CreatePayment`, `GetPayment`, `Mode`), types `PaymentRequest` / `PaymentInfo`, statuts (`open`, `pending`, `authorized`, `paid`, `canceled`, `expired`, `failed`), conversion centimes ↔ montant décimal (`FormatAmount`, `ParseAmount`). |
| `mollie.go` | `MollieClient` : API Payments v2 de Mollie. Le mode (`test` / `live`) dépend uniquement du préfixe de la clé (`test_…` / `live_…`). |

## Fonctionnement

1. `api.topup` appelle `CreatePayment` avec le montant, l'URL de retour
   (`${PUBLIC_URL}/?billing_return=1`) et l'URL de webhook, puis renvoie
   `CheckoutURL` au frontend.
2. Mollie notifie la webhook ou le frontend appelle `/api/billing/sync` ;
   `GetPayment` relit alors le statut, et `store.MarkPaymentPaid` crédite le
   compte une seule fois.

## Dépendances

Bibliothèque standard uniquement (`net/http`, `encoding/json`).

## Points d'attention

- Une URL de webhook **locale** (`localhost`, `127.0.0.1`, `::1`,
  `*.localhost`) n'est pas transmise à Mollie, qui la refuserait.
- Les montants circulent en **centimes** (`int64`) dans tout le code ; la
  conversion en décimal (`"12.34"`) n'a lieu qu'à la frontière de l'API Mollie.
- Un autre prestataire (Stripe…) s'ajouterait en implémentant
  `PaymentProvider`, sans toucher aux handlers.

## Tests

`go test ./internal/billing/` — client Mollie contre un serveur `httptest`,
conversions de montants.
