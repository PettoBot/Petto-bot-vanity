-- Preserve existing role ownership evidence in the grant ledger so stale
-- bot-managed roles remain removable even if a role-state row is later lost.
UPDATE identity_role_grants AS grants
SET bot_added_role = true,
    updated_at = now()
FROM identity_role_state AS state
WHERE grants.guild_id = state.guild_id
  AND grants.user_id = state.user_id
  AND grants.role_id = state.role_id
  AND grants.action = 'add_role'
  AND state.bot_added_role = true
  AND grants.bot_added_role = false;
