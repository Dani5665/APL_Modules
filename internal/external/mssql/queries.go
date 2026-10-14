package mssql

// All SQL sent to the external database lives in this file so the queries can
// be adjusted without touching the surrounding Go code.
//
// Every query is parameterised (@p1, @p2, ...). Never build SQL by
// concatenating caller-supplied values.
//
// Confirmed against production: [STORE_IT_APL_PROD].[dbo].[V_CATALOG_USERS]
// is a view over the CATALOG_USERS table used in the original specification,
// and carries more than that table's four columns:
//
//	LOGIN            a username - the salesperson's for IS_WHO_SALER = 1,
//	                 one of a client's own for IS_WHO_SALER = 0
//	CUSTOMER_NUMBER  the 9-digit client code (only meaningful when
//	                 IS_WHO_SALER = 0)
//	CUSTOMER_NAME    the client's name        (IS_WHO_SALER = 0 rows only)
//	MANDANT_NAME     the STORE the row belongs to, for both salesperson and
//	                 client rows - despite the name, this is not the client's
//	                 name (see DECISIONS.md for how that was established)
//	IS_WHO_SALER     1 for a salesperson row, 0 for a client's own username
//	ACTIVE           1 for a row that is currently active; every query here
//	                 filters to ACTIVE = 1
//
// This view supplies everything PLACEHOLDER-B and PLACEHOLDER-C asked for.
// The client's "Обект на клиента" (object) has no column of its own here -
// confirmed with the project owner, MANDANT_NAME is used for it as well as
// for the store, so Client.Object and Client.Store carry the same value; see
// external.go.

// qSaler returns a salesperson's store for the LOGIN passed as @p1.
const qSaler = `
SELECT TOP (1) [LOGIN], [MANDANT_NAME]
FROM [STORE_IT_APL_PROD].[dbo].[V_CATALOG_USERS]
WHERE [LOGIN] = @p1 AND [IS_WHO_SALER] = 1 AND [ACTIVE] = 1
`

// qClientByCode returns a client's name and store for the 9-digit code
// passed as @p1. The caller also uses this same MANDANT_NAME value for the
// client's "Обект на клиента" (Client.Object); see external.go.
const qClientByCode = `
SELECT TOP (1) [CUSTOMER_NUMBER], [CUSTOMER_NAME], [MANDANT_NAME], [MANDANT_ID]
FROM [STORE_IT_APL_PROD].[dbo].[V_CATALOG_USERS]
WHERE [CUSTOMER_NUMBER] = @p1 AND [IS_WHO_SALER] = 0 AND [ACTIVE] = 1
`

// qClientLogins lists a client's own usernames for the 9-digit code passed
// as @p1.
const qClientLogins = `
SELECT [LOGIN]
FROM [STORE_IT_APL_PROD].[dbo].[V_CATALOG_USERS]
WHERE [CUSTOMER_NUMBER] = @p1 AND [IS_WHO_SALER] = 0 AND [ACTIVE] = 1
ORDER BY [LOGIN]
`

// qClientsByStoresTemplate lists every client tied to any of the given
// stores, for the informational "Clients of my stores" list. The store
// values are bound as @p1, @p2, ... in place of the inParamsMarker below.
// A client contributes one row per username, so the rows are grouped per
// client and the list is sorted newest first by the latest CREATED_ON among
// its rows (a plain DISTINCT could not also sort by a column outside the
// select list). Ties fall back to the name and number, to keep the order
// stable.
const qClientsByStoresTemplate = `
SELECT [CUSTOMER_NUMBER], [CUSTOMER_NAME], [MANDANT_NAME], [MANDANT_ID]
FROM [STORE_IT_APL_PROD].[dbo].[V_CATALOG_USERS]
WHERE [IS_WHO_SALER] = 0 AND [ACTIVE] = 1 AND [MANDANT_ID] IN (/*IN*/)
GROUP BY [CUSTOMER_NUMBER], [CUSTOMER_NAME], [MANDANT_NAME], [MANDANT_ID]
ORDER BY MAX([CREATED_ON]) DESC, [CUSTOMER_NAME], [CUSTOMER_NUMBER]
`

// inParamsMarker is replaced by the parameterised IN list in
// qClientsByStoresTemplate.
const inParamsMarker = `/*IN*/`

// V_EXT_CUSTOMERS_INFO has several rows per customer, all carrying the same
// information; Is_Customer = 1 limits it to customers. Empty values come back
// as NULL or ''; both are shown as "-" by the page.

// qCustomers lists the customers. The caller keeps the first row per
// Customer_Code (external.FirstPerCode).
const qCustomers = `
SELECT [Customer_Code], [Customer_Name], [Mandant_Name], [Customer_Address], [Phone_Number], [Accountable_Person]
FROM [STORE_IT_APL_PROD].[dbo].[V_EXT_CUSTOMERS_INFO]
WHERE [Is_Customer] = 1
ORDER BY [Customer_Name], [Customer_Code]
`

// qCustomerByCode returns the first row for the Customer_Code in @p1.
const qCustomerByCode = `
SELECT TOP (1) [Customer_Code], [Customer_Name], [Mandant_Name], [Customer_Address], [Phone_Number], [Accountable_Person]
FROM [STORE_IT_APL_PROD].[dbo].[V_EXT_CUSTOMERS_INFO]
WHERE [Is_Customer] = 1 AND [Customer_Code] = @p1
`

// qProfiles lists a customer's own catalog accounts for the number in @p1.
// ACTIVE is selected rather than filtered on, so inactive profiles show with
// their status.
const qProfiles = `
SELECT [LOGIN], [PASSWORD], [MANDANT_NAME], [LAST_LOGIN], [ACTIVE]
FROM [STORE_IT_APL_PROD].[dbo].[V_CATALOG_USERS]
WHERE [CUSTOMER_NUMBER] = @p1 AND [IS_WHO_SALER] = 0
ORDER BY [LOGIN]
`

// qLoginExists finds an account with the LOGIN in @p1. SQL Server compares
// case-insensitively with its default collation, which is what a "taken"
// check needs.
const qLoginExists = `
SELECT TOP (1) 1
FROM [STORE_IT_APL_PROD].[dbo].[V_CATALOG_USERS]
WHERE [LOGIN] = @p1
`

// qTransports lists the transport options of the serving store whose
// MANDANT_ID is in @p1.
const qTransports = `
SELECT [BESCHREIBUNG]
FROM [STORE_IT_APL_PROD].[dbo].[V_VERSART]
WHERE [MANDANT_ID] = @p1
ORDER BY [BESCHREIBUNG]
`
