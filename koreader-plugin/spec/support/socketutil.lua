-- Stand-in for KOReader's socketutil: no timeouts to set without sockets.
return {
    USER_AGENT = "Liseur plugin spec",
    set_timeout = function() end,
    reset_timeout = function() end,
}
