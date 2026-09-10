-- Earlier releases stored a tray default but did not apply it. Use an explicit
-- ask state so upgrading users see the new confirmation before a preference is
-- remembered.
UPDATE system_settings
SET close_behavior = 'ask';
