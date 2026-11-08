-- Stand-in for LTN12 outside KOReader: the transport only builds a string
-- source and a filtering sink, and neither needs to run for real.
return { source = { string = function(s) return s end }, sink = { table = function() return {} end } }
