package audit

// Phase 27: SMS credits bought with mobile money (Snippe), and the platform's
// own SMS stock. Kept apart from the main action list so the phase's names
// sit together.
const (
	// ActionSMSCreditOrder — a landlord asked Snippe to push a payment prompt.
	ActionSMSCreditOrder = "sms_credits.order"
	// ActionSMSCreditPurchase — a paid order credited the org. No actor: the
	// webhook or the reconciliation job did it.
	ActionSMSCreditPurchase = "sms_credits.purchase"
	// ActionSMSPackageCreate / Update — the platform edits what is on sale.
	ActionSMSPackageCreate = "sms_package.create"
	ActionSMSPackageUpdate = "sms_package.update"
	// ActionPlatformSMSPurchase — the platform recorded a Beem bundle.
	ActionPlatformSMSPurchase = "platform_sms.purchase"
	// ActionSMSOrderReconcile — an admin ran the order reconciliation now.
	ActionSMSOrderReconcile = "sms_orders.reconcile"

	EntitySMSCreditOrder      = "sms_credit_order"
	EntitySMSCreditPackage    = "sms_credit_package"
	EntityPlatformSMSPurchase = "platform_sms_purchase"
)
