ALTER TABLE telegram_accounts
    DROP COLUMN notify_chat_id,
    DROP COLUMN notify_verify_code,
    DROP COLUMN notify_on_lead,
    DROP COLUMN notify_on_payment;
