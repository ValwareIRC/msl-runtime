; A first script for the mIRC runtime.
on *:START:{ echo -s Runtime started with $script(0) script(s). Connect with: server irc.example.net +6697 }
on *:CONNECT:{ echo -s Connected to $network as $me }
on *:JOIN:#:{ if ($nick == $me) echo -s Joined $chan with $nick($chan,0) users }
on *:TEXT:!ping:#:{ msg $chan pong, $nick }
alias hello { echo -a Hello from an alias, $1- }
