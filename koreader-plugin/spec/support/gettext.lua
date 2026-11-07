-- Test stand-in for KOReader's gettext: strings are returned unchanged.
local gettext = setmetatable({}, { __call = function(_, s) return s end })
function gettext.changeLang() end
return gettext
