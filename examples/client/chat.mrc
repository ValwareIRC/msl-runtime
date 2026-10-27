; ---------------------------------------------------------------------------
; mslrt chat - a modern IRC client written in mIRC script.
;
; Run:   mslrt examples/client/chat.mrc
; Then type in the box at the bottom, for example:
;        /server irc.ergo.chat +6697
;        /join #chat
;
; IRCv3: typing notifications (+typing), replies (+draft/reply) and
; reactions (+draft/react) need a server with message-tags, such as Ergo.
; Left-click a message to reply to it, right-click it to react, click a
; reaction to add or remove yours, and scroll with the mouse wheel.
; ---------------------------------------------------------------------------

; Layout and colours -------------------------------------------------------

alias -l c.w return 960
alias -l c.h return 610
alias -l c.side return 230
alias -l c.top return 56
alias -l c.font return Segoe UI
alias -l c.bg return $rgb(49,51,56)
alias -l c.sidebg return $rgb(30,31,34)
alias -l c.text return $rgb(219,222,225)
alias -l c.muted return $rgb(148,155,164)
alias -l c.accent return $rgb(88,101,242)
alias -l c.line return $rgb(38,40,44)
alias -l c.chip return $rgb(43,45,49)
alias -l c.mine return $rgb(55,59,107)
alias -l c.mention return $rgb(65,59,44)

; A pleasant, stable colour for each nick.
alias -l c.nickcolor {
  var %c = $rgb(242,63,66) $rgb(240,178,50) $rgb(35,165,90) $rgb(0,168,252) $rgb(235,69,158) $rgb(155,132,236) $rgb(26,188,156) $rgb(230,126,34)
  return $gettok(%c, $calc($hash($1,8) % 8 + 1), 32)
}

; Buffers --------------------------------------------------------------------
; A buffer is a conversation keyed "cid:target" ("cid:*" is the server).
; Its messages live in the hash table chat:<key> as N.nick, N.text, N.time,
; N.kind (msg, action, notice, info), N.id (msgid), N.reply (msgid),
; N.pending (own message not yet echoed), N.rl (reaction emoji, in order)
; and N.r.<emoji> (nicks who reacted).

alias -l chat.t return chat: $+ $1

alias -l chat.key {
  ; $chat.key(cid,target)
  return $1 $+ : $+ $2
}

alias -l chat.ensure {
  ; /chat.ensure key - add a buffer if it is new
  if (!$istok(%chat.bufs,$1,32)) {
    set %chat.bufs $addtok(%chat.bufs,$1,32)
    hmake $chat.t($1) 100
    hadd $chat.t($1) n 0
  }
}

alias -l chat.drop {
  if (!$istok(%chat.bufs,$1,32)) return
  set %chat.bufs $remtok(%chat.bufs,$1,1,32)
  hfree $chat.t($1)
  hdel -w chat.unread $1
  if (%chat.cur == $1) chat.switch $gettok(%chat.bufs,1,32)
}

alias -l chat.switch {
  if ($1 == $null) return
  set %chat.cur $1
  set %chat.scroll 0
  unset %chat.reply
  hdel chat.unread $1
  chat.draw
}

; /chat.add key nick kind id reply text...
alias -l chat.add {
  var %key = $1, %t = $chat.t($1)
  chat.ensure %key
  var %n = $calc($hget(%t,n) + 1)
  hadd %t n %n
  hadd %t %n $+ .nick $2
  hadd %t %n $+ .kind $3
  hadd %t %n $+ .time $ctime
  if ($4 != -) {
    hadd %t %n $+ .id $4
    hadd chat.ids $4 %key %n
  }
  if ($5 != -) hadd %t %n $+ .reply $5
  hadd %t %n $+ .text $6-
  if (%key != %chat.cur) {
    if ($3 != info) hinc chat.unread %key
  }
  elseif (%chat.scroll > 0) inc %chat.scroll
  return %n
}

; /chat.info key text - a muted system line
alias -l chat.info chat.add $1 * info - - $2-

; The buffer for a message event: the channel, or the other person.
alias -l chat.here {
  if ($chan) return $chat.key($cid,$chan)
  if ($nick == $me) return $chat.key($cid,$target)
  return $chat.key($cid,$nick)
}

; Find a message by msgid: returns "key N".
alias -l chat.byid return $hget(chat.ids,$1)

; Reactions ------------------------------------------------------------------

; /chat.react key N nick emoji [remove]
alias -l chat.react {
  var %t = $chat.t($1), %n = $2, %item = %n $+ .r. $+ $4
  var %who = $hget(%t,%item)
  if ($5) var %who = $remtok(%who,$3,1,32)
  else var %who = $addtok(%who,$3,32)
  if (%who == $null) {
    hdel %t %item
    hadd %t %n $+ .rl $remtok($hget(%t,%n $+ .rl),$4,1,32)
  }
  else {
    hadd %t %item %who
    hadd %t %n $+ .rl $addtok($hget(%t,%n $+ .rl),$4,32)
  }
}

; /chat.toggle key N emoji - add or remove my reaction and tell the server.
alias -l chat.toggle {
  var %t = $chat.t($1), %id = $hget(%t,$2 $+ .id)
  var %target = $gettok($1,2-,58), %cid = $gettok($1,1,58)
  var %mine = $istok($hget(%t,$2 $+ .r. $+ $3),$scid(%cid).me,32)
  chat.react $1 $2 $scid(%cid).me $3 %mine
  if (%id != $null) {
    if (%mine) scid %cid raw @+draft/unreact= $+ $3 $+ ;+draft/reply= $+ %id TAGMSG %target
    else scid %cid raw @+draft/react= $+ $3 $+ ;+draft/reply= $+ %id TAGMSG %target
  }
  chat.draw
}

; Drawing --------------------------------------------------------------------

alias -l chat.line { return $height(Xg,$c.font,11) }

; Whether message N starts a new group (shows avatar and name).
alias -l chat.groupstart {
  var %t = $1, %n = $2
  if (%n <= 1) return 1
  if ($hget(%t,%n $+ .kind) == info) return 0
  if ($hget(%t,%n $+ .reply) != $null) return 1
  var %p = $calc(%n - 1)
  if ($hget(%t,%p $+ .kind) == info) return 1
  if ($hget(%t,%p $+ .nick) != $hget(%t,%n $+ .nick)) return 1
  if ($calc($hget(%t,%n $+ .time) - $hget(%t,%p $+ .time)) > 300) return 1
  return 0
}

alias -l chat.textw return $calc($c.w - $c.side - 92)

; Whether text mentions a nick as a whole word.
alias -l chat.mentions {
  var %words = $replace($strip($1),$chr(44),$chr(32),.,$chr(32),!,$chr(32),?,$chr(32),:,$chr(32),;,$chr(32))
  return $istok(%words,$2,32)
}

; Height in pixels of message N.
alias -l chat.height {
  var %t = $1, %n = $2, %lh = $chat.line
  if ($hget(%t,%n $+ .kind) == info) return $calc(%lh + 8)
  var %h = $calc($wrap($hget(%t,%n $+ .text),$c.font,11,$chat.textw,p0,0) * %lh + 4)
  if ($chat.groupstart(%t,%n)) inc %h $calc(%lh + 14)
  if ($hget(%t,%n $+ .reply) != $null) inc %h 20
  if ($hget(%t,%n $+ .rl) != $null) inc %h 30
  return %h
}

; /chat.hit x y w h action... - remember a clickable area
alias -l chat.hit {
  hinc chat.hits n
  hadd chat.hits $hget(chat.hits,n) $1-
}

alias chat.draw {
  if (!$window(@chat)) return
  if ($hget(chat.hits)) hfree chat.hits
  hmake chat.hits 50
  var %W = $c.w, %H = $c.h, %S = $c.side
  drawrect -rfn @chat $c.bg 1 0 0 %W %H
  chat.drawside
  chat.drawheader
  chat.drawmessages
  chat.drawfooter
  drawdot @chat
}

alias -l chat.drawside {
  var %S = $c.side, %y = 16
  drawrect -rfn @chat $c.sidebg 1 0 0 %S $c.h
  drawrect -rfdn @chat $c.accent 1 16 %y 10 10 10 10
  drawtext -ron @chat $c.text "Segoe UI" 13 34 $calc(%y - 5) mslrt chat
  inc %y 30
  var %i = 1, %lastcid = 0
  while ($gettok(%chat.bufs,%i,32) != $null) {
    var %key = $v1, %cid = $gettok(%key,1,58), %target = $gettok(%key,2-,58)
    if (%cid != %lastcid) {
      inc %y 8
      var %net = $iif($scid(%cid).network,$v1,server)
      drawtext -rn @chat $c.muted "Segoe UI" 9 16 %y $upper(%net)
      inc %y 22
      var %lastcid = %cid
    }
    if (%key == %chat.cur) drawrect -rfdn @chat $rgb(64,66,73) 1 8 %y $calc(%S - 16) 28 8 8
    var %label = $iif(%target == *,status,%target)
    var %color = $iif(%key == %chat.cur,$rgb(242,243,245),$c.muted)
    var %unread = $hget(chat.unread,%key)
    if (%unread) var %color = $rgb(242,243,245)
    drawtext -r $+ $iif(%unread,o) $+ n @chat %color "Segoe UI" 11 22 $calc(%y + 5) %label
    if (%unread) {
      var %bw = $calc($width(%unread,$c.font,9,b1) + 12)
      drawrect -rfdn @chat $rgb(242,63,66) 1 $calc(%S - 18 - %bw) $calc(%y + 6) %bw 17 16 16
      drawtext -ron @chat $rgb(255,255,255) "Segoe UI" 9 $calc(%S - 12 - %bw) $calc(%y + 7) %unread
    }
    chat.hit 8 %y $calc(%S - 16) 28 switch %key
    inc %y 32
    inc %i
  }
  if (%chat.bufs == $null) drawtext -rn @chat $c.muted "Segoe UI" 10 16 %y Not connected
}

alias -l chat.drawheader {
  var %S = $c.side, %key = %chat.cur
  drawline -rn @chat $c.line 1 %S $c.top $c.w $c.top
  if (%key == $null) return
  var %cid = $gettok(%key,1,58), %target = $gettok(%key,2-,58)
  var %title = $iif(%target == *,$iif($scid(%cid).network,$v1,Server),%target)
  drawtext -ron @chat $rgb(242,243,245) "Segoe UI" 13 $calc(%S + 20) 16 %title
  var %x = $calc(%S + 34 + $width(%title,$c.font,13,b1))
  if ($left(%target,1) isin $scid(%cid).chantypes) {
    scid %cid
    var %info = $nick(%target,0) members
    if ($chan(%target).topic != $null) var %info = %info $chr(183) $strip($v1)
    scid -r
    var %fit = $wrap(%info,$c.font,10,$calc($c.w - %x - 20),p0,1)
    if (%fit != %info) var %fit = $left(%fit,-1) $+ $chr(8230)
    drawtext -rn @chat $c.muted "Segoe UI" 10 %x 19 %fit
  }
}

alias -l chat.drawmessages {
  var %key = %chat.cur
  if (%key == $null) {
    chat.welcome
    return
  }
  var %t = $chat.t(%key), %n = $hget(%t,n), %top = $calc($c.top + 8)
  var %bottom = $calc($c.h - 34 - $iif(%chat.reply,32,0))
  var %y = %bottom, %i = $calc(%n - %chat.scroll)
  if (%n == 0) drawtext -rn @chat $c.muted "Segoe UI" 11 $calc($c.side + 20) $calc($c.top + 20) This is the start of the conversation.
  while (%i >= 1) {
    var %h = $chat.height(%t,%i)
    dec %y %h
    if (%y < %top) break
    chat.drawmsg %t %i %y %h %key
    dec %i
  }
  if (%chat.scroll > 0) {
    var %bx = $calc($c.w - 170)
    drawrect -rfdn @chat $c.accent 1 %bx $calc(%bottom - 30) 150 24 12 12
    drawtext -rn @chat $rgb(255,255,255) "Segoe UI" 10 $calc(%bx + 18) $calc(%bottom - 26) Jump to latest
    chat.hit %bx $calc(%bottom - 30) 150 24 latest
  }
}

; /chat.drawmsg table N y height key
alias -l chat.drawmsg {
  var %t = $1, %n = $2, %y = $3, %h = $4, %key = $5
  var %S = $c.side, %lh = $chat.line, %x = $calc(%S + 72)
  var %nick = $hget(%t,%n $+ .nick), %text = $hget(%t,%n $+ .text), %kind = $hget(%t,%n $+ .kind)
  var %cid = $gettok(%key,1,58)
  if (%kind == info) {
    drawtext -rn @chat $c.muted "Segoe UI" 10 %x $calc(%y + 4) $chr(8594) %text
    return
  }
  ; Highlight replies being written, and lines that mention me.
  if (%chat.reply == %key %n) drawrect -rfn @chat $rgb(56,58,64) 1 %S %y $calc($c.w - %S) %h
  elseif ($chat.mentions(%text,$scid(%cid).me)) && (%nick != $scid(%cid).me) {
    drawrect -rfn @chat $c.mention 1 %S %y $calc($c.w - %S) %h
    drawrect -rfn @chat $rgb(240,178,50) 1 %S %y 3 %h
  }
  chat.hit %S %y $calc($c.w - %S) %h msg %key %n
  var %yy = %y
  var %reply = $hget(%t,%n $+ .reply)
  if (%reply != $null) {
    inc %yy 4
    var %src = $chat.byid(%reply), %quote = original message not loaded
    if (%src != $null) {
      var %rt = $chat.t($gettok(%src,1,32)), %rn = $gettok(%src,2,32)
      var %quote = $hget(%rt,%rn $+ .nick) $+ : $hget(%rt,%rn $+ .text)
      var %quote = $wrap(%quote,$c.font,9,$calc($chat.textw - 30),p0,1)
    }
    drawline -rn @chat $rgb(78,80,88) 2 $calc(%S + 36) $calc(%yy + 14) $calc(%S + 36) $calc(%yy + 8) $calc(%x - 6) $calc(%yy + 8)
    drawtext -rn @chat $c.muted "Segoe UI" 9 %x %yy %quote
    inc %yy 16
  }
  if ($chat.groupstart(%t,%n)) {
    inc %yy 8
    var %col = $c.nickcolor(%nick)
    drawrect -refn @chat %col 1 $calc(%S + 16) %yy 40 40
    var %initial = $upper($left($remove(%nick,_,-,[,],^,|,`,\),1))
    if (%initial == $null) var %initial = $upper($left(%nick,1))
    drawtext -ron @chat $rgb(255,255,255) "Segoe UI" 15 $calc(%S + 36 - $width(%initial,$c.font,15,b1) / 2) $calc(%yy + 8) %initial
    drawtext -ron @chat %col "Segoe UI" 11 %x %yy %nick
    var %stamp = $asctime($hget(%t,%n $+ .time),HH:nn)
    if ($hget(%t,%n $+ .pending)) var %stamp = %stamp $chr(183) sending
    drawtext -rn @chat $c.muted "Segoe UI" 9 $calc(%x + $width(%nick,$c.font,11,b1) + 10) $calc(%yy + 3) %stamp
    inc %yy $calc(%lh + 4)
  }
  var %color = $c.text
  if (%kind == action) var %text = $chr(42) %nick %text
  if (%kind == notice) var %color = $rgb(240,178,50)
  if ($hget(%t,%n $+ .pending)) var %color = $c.muted
  var %lines = $wrap(%text,$c.font,11,$chat.textw,p0,0), %j = 1
  while (%j <= %lines) {
    drawtext -rn @chat %color "Segoe UI" 11 %x %yy $wrap(%text,$c.font,11,$chat.textw,p0,%j)
    inc %yy %lh
    inc %j
  }
  ; Reaction chips.
  var %rl = $hget(%t,%n $+ .rl)
  if (%rl != $null) {
    inc %yy 6
    var %k = 1, %cx = %x, %me = $scid(%cid).me
    while ($gettok(%rl,%k,32) != $null) {
      var %emoji = $v1, %who = $hget(%t,%n $+ .r. $+ %emoji)
      var %label = %emoji $numtok(%who,32)
      var %cw = $calc($width(%label,$c.font,10) + 20)
      var %mine = $istok(%who,%me,32)
      drawrect -rfdn @chat $iif(%mine,$c.mine,$c.chip) 1 %cx %yy %cw 24 12 12
      if (%mine) drawrect -rdn @chat $c.accent 1 %cx %yy %cw 24 12 12
      drawtext -rn @chat $c.text "Segoe UI" 10 $calc(%cx + 10) $calc(%yy + 4) %label
      chat.hit %cx %yy %cw 24 react %key %n %emoji
      inc %cx $calc(%cw + 6)
      inc %k
    }
  }
}

alias -l chat.drawfooter {
  var %S = $c.side, %y = $calc($c.h - 30)
  if (%chat.reply != $null) {
    var %key = $gettok(%chat.reply,1,32), %n = $gettok(%chat.reply,2,32), %t = $chat.t(%key)
    var %by = $calc(%y - 30)
    drawrect -rfn @chat $c.chip 1 %S %by $calc($c.w - %S) 30
    var %about = Replying to $hget(%t,%n $+ .nick) $+ : $hget(%t,%n $+ .text)
    var %about = $wrap(%about,$c.font,10,$calc($c.w - %S - 80),p0,1)
    drawtext -rn @chat $c.muted "Segoe UI" 10 $calc(%S + 16) $calc(%by + 7) %about
    drawtext -ron @chat $c.text "Segoe UI" 12 $calc($c.w - 30) $calc(%by + 5) $chr(215)
    chat.hit $calc($c.w - 40) %by 40 30 noreply
  }
  var %typing = $chat.typers(%chat.cur)
  if (%typing != $null) {
    var %who = $replace(%typing,$chr(32),$chr(44) $+ $chr(32))
    var %verb = $iif($numtok(%typing,32) > 1,are,is)
    if ($numtok(%typing,32) > 3) var %who = Several people, %verb = are
    drawtext -rn @chat $c.text "Segoe UI" 9 $calc(%S + 20) $calc(%y + 8) $chr(8226) $+ $chr(8226) $+ $chr(8226) %who %verb typing $+ $chr(8230)
  }
}

alias -l chat.welcome {
  var %x = $calc($c.side + 40), %y = 110
  drawtext -ron @chat $rgb(242,243,245) "Segoe UI" 20 %x %y Welcome to mslrt chat
  inc %y 44
  var %lines = Connect to a server: /server irc.ergo.chat +6697 $+ $chr(10) $+ Join a channel: /join #chat $+ $chr(10) $+ Change your name: /nick newname
  var %lines = %lines $+ $chr(10) $+ Left-click a message to reply, right-click to react. $+ $chr(10) $+ Scroll with the mouse wheel.
  var %i = 1
  while ($gettok(%lines,%i,10) != $null) {
    drawtext -rn @chat $c.muted "Segoe UI" 11 %x %y $v1
    inc %y 26
    inc %i
  }
}

; Typing ---------------------------------------------------------------------
; chat.typing holds key<BEL>nick items with the $ctime they last typed.

alias -l chat.typers {
  var %i = 1, %out
  while ($hget(chat.typing,%i).item != $null) {
    var %item = $v1
    if ($gettok(%item,1,7) == $1) && ($calc($ctime - $hget(chat.typing,%item)) <= 6) var %out = $addtok(%out,$gettok(%item,2,7),32)
    inc %i
  }
  return %out
}

alias chat.tick {
  ; Expire stale typing notices and redraw when the indicator changes.
  var %before = $chat.typers(%chat.cur), %i = $hget(chat.typing,0).item
  while (%i >= 1) {
    var %item = $hget(chat.typing,%i).item
    if ($calc($ctime - $hget(chat.typing,%item)) > 6) hdel chat.typing %item
    dec %i
  }
  if ($chat.typers(%chat.cur) != %before) chat.draw
}

; /chat.notifytyping - tell the current conversation whether I am typing.
alias -l chat.notifytyping {
  var %key = %chat.cur, %text = $editbox(@chat)
  if (%key == $null) || ($gettok(%key,2-,58) == *) return
  var %cid = $gettok(%key,1,58), %target = $gettok(%key,2-,58)
  if ($scid(%cid).status != connected) return
  if (%text == $null) || ($left(%text,1) == /) {
    if (%chat.typing.sent == %key) {
      scid %cid raw @+typing=done TAGMSG %target
      unset %chat.typing.sent %chat.typing.at
    }
    return
  }
  if (%chat.typing.sent != %key) || ($calc($ctime - %chat.typing.at) >= 3) {
    scid %cid raw @+typing=active TAGMSG %target
    set %chat.typing.sent %key
    set %chat.typing.at $ctime
  }
}

; Sending --------------------------------------------------------------------

; /chat.send text - send to the current conversation, as a reply if one is chosen.
alias -l chat.send {
  var %key = %chat.cur
  if (%key == $null) || ($gettok(%key,2-,58) == *) {
    chat.info $iif(%key,%key,$chat.key($activecid,*)) Choose a channel or conversation first, or type a /command.
    return
  }
  var %cid = $gettok(%key,1,58), %target = $gettok(%key,2-,58), %reply = -, %text = $1-
  if (%chat.reply != $null) {
    var %rt = $chat.t($gettok(%chat.reply,1,32)), %rn = $gettok(%chat.reply,2,32)
    var %rid = $hget(%rt,%rn $+ .id)
    if (%rid != $null) var %reply = %rid
    else var %text = $hget(%rt,%rn $+ .nick) $+ : %text
  }
  if (%reply != -) scid %cid raw @+draft/reply= $+ %reply PRIVMSG %target : $+ %text
  else scid %cid msg %target %text
  var %n = $chat.add(%key,$scid(%cid).me,msg,-,%reply,%text)
  hadd $chat.t(%key) %n $+ .pending 1
  unset %chat.reply %chat.typing.sent
  set %chat.scroll 0
  chat.draw
}

; An echoed copy of my own message: give the pending copy its msgid.
alias -l chat.echoed {
  var %key = $1, %t = $chat.t($1), %id = $2, %i = $hget(%t,n)
  while (%i >= 1) {
    if ($hget(%t,%i $+ .pending)) && ($hget(%t,%i $+ .text) == $3-) {
      hdel %t %i $+ .pending
      if (%id != $null) {
        hadd %t %i $+ .id %id
        hadd chat.ids %id %key %i
      }
      return 1
    }
    dec %i
  }
  return 0
}

; Window and input -----------------------------------------------------------

alias chat {
  if (!$window(@chat)) {
    window -pek0 @chat -1 -1 $c.w $calc($c.h + 30)
    renwin @chat @chat mslrt chat
  }
  window -a @chat
  if (!$hget(chat.ids)) hmake chat.ids 200
  if (!$hget(chat.unread)) hmake chat.unread 20
  if (!$hget(chat.typing)) hmake chat.typing 20
  .timerchat.tick 0 1 chat.tick
  chat.draw
}

on *:START:{
  if ($mnick == $null) || ($mnick == mslrt) mnick chat $+ $rand(1000,9999)
  unset %chat.*
  chat
}

on *:INPUT:@chat:{
  var %text = $1-
  if (%text == $null) return
  if ($left(%text,1) == /) {
    if ($1 == /react) {
      chat.reactcmd $2
      return
    }
    ; Run other commands as typed, on the current conversation's connection.
    var %cid = $gettok(%chat.cur,1,58)
    if (%cid isnum 1-) scid %cid
    $right(%text,-1)
    chat.notifytyping
    return
  }
  chat.send %text
}

; /react emoji - react to the message being replied to, or the latest one.
alias -l chat.reactcmd {
  if ($1 == $null) || (%chat.cur == $null) return
  if (%chat.reply != $null) chat.toggle $gettok(%chat.reply,1,32) $gettok(%chat.reply,2,32) $1
  else chat.toggle %chat.cur $hget($chat.t(%chat.cur),n) $1
  unset %chat.reply
}

on *:KEYDOWN:@chat:*:{ chat.notifytyping }

on *:CLOSE:@:{
  if ($target == @chat) .timerchat.tick off
}

; Mouse: hit areas recorded while drawing.
alias -l chat.at {
  var %i = 1
  while ($hget(chat.hits,%i) != $null) {
    var %h = $v1
    if ($inrect($mouse.x,$mouse.y,$gettok(%h,1,32),$gettok(%h,2,32),$gettok(%h,3,32),$gettok(%h,4,32))) var %found = $gettok(%h,5-,32)
    inc %i
  }
  return %found
}

alias chat.click {
  var %a = $chat.at
  tokenize 32 %a
  if ($1 == switch) chat.switch $2
  elseif ($1 == react) chat.toggle $2 $3 $4
  elseif ($1 == noreply) { unset %chat.reply | chat.draw }
  elseif ($1 == latest) { set %chat.scroll 0 | chat.draw }
  elseif ($1 == msg) {
    if (%chat.reply == $2 $3) unset %chat.reply
    else set %chat.reply $2 $3
    chat.draw
  }
}

alias chat.rclick {
  tokenize 32 $chat.at
  if ($1 == msg) set %chat.ctx $2 $3
  else unset %chat.ctx
}

alias chat.wheel {
  var %n = $hget($chat.t(%chat.cur),n)
  if ($mouse.dy < 0) set %chat.scroll $calc(%chat.scroll + 2)
  elseif ($mouse.dy > 0) set %chat.scroll $calc(%chat.scroll - 2)
  if (%chat.scroll > $calc(%n - 1)) set %chat.scroll $calc(%n - 1)
  if (%chat.scroll < 0) set %chat.scroll 0
  chat.draw
}

alias chat.ctxreact { if (%chat.ctx) chat.toggle %chat.ctx $1 }

menu @chat {
  sclick:chat.click
  rclick:chat.rclick
  wheel:chat.wheel
  $iif(%chat.ctx,Reply):{ set %chat.reply %chat.ctx | chat.draw }
  -
  $iif(%chat.ctx,React thumbs up):chat.ctxreact 👍
  $iif(%chat.ctx,React heart):chat.ctxreact ❤️
  $iif(%chat.ctx,React laughing):chat.ctxreact 😂
  $iif(%chat.ctx,React party):chat.ctxreact 🎉
  $iif(%chat.ctx,React eyes):chat.ctxreact 👀
}

; IRC events -----------------------------------------------------------------

on *:CONNECT:{
  ; Ask for our own messages back with their msgid, so they can be replied to.
  raw CAP REQ :echo-message
  var %key = $chat.key($cid,*)
  chat.ensure %key
  chat.info %key Connected to $network as $me
  if (%chat.cur == $null) || ($gettok(%chat.cur,1,58) == 0) chat.switch %key
  else chat.draw
}

on *:DISCONNECT:{
  var %i = 1
  while ($gettok(%chat.bufs,%i,32) != $null) {
    if ($gettok($v1,1,58) == $cid) chat.info $v1 Disconnected.
    inc %i
  }
  chat.draw
}

on *:JOIN:#:{
  var %key = $chat.key($cid,$chan)
  if ($nick == $me) {
    chat.ensure %key
    chat.info %key You joined $chan
    chat.switch %key
    return
  }
  chat.info %key $nick joined
  if (%key == %chat.cur) chat.draw
}

on *:PART:#:{
  var %key = $chat.key($cid,$chan)
  if ($nick == $me) {
    chat.drop %key
    chat.draw
    return
  }
  chat.info %key $nick left $iif($1-,( $+ $1- $+ ))
  if (%key == %chat.cur) chat.draw
}

on *:KICK:#:{
  var %key = $chat.key($cid,$chan)
  chat.info %key $knick was kicked by $nick $iif($1-,( $+ $1- $+ ))
  chat.draw
}

on *:QUIT:{
  var %i = 1
  while ($comchan($nick,%i) != $null) {
    chat.info $chat.key($cid,$v1) $nick quit $iif($1-,( $+ $1- $+ ))
    inc %i
  }
  chat.draw
}

on *:NICK:{
  var %i = 1
  while ($comchan($nick,%i) != $null) {
    chat.info $chat.key($cid,$v1) $nick is now known as $newnick
    inc %i
  }
  chat.draw
}

on *:TOPIC:#:{
  chat.info $chat.key($cid,$chan) $nick changed the topic to: $1-
  chat.draw
}

raw 332:*:{ chat.draw }
raw 366:*:{ chat.draw }

on *:TEXT:*:*:{ chat.incoming msg $1- }
on *:ACTION:*:*:{ chat.incoming action $1- }
on *:NOTICE:*:*:{
  if ($nick == $null) || (. isin $nick) {
    chat.info $chat.key($cid,*) $1-
    chat.draw
    return
  }
  chat.incoming notice $1-
}

alias -l chat.incoming {
  var %key = $chat.here, %id = $msgtags(msgid), %reply = $msgtags(+draft/reply)
  hdel chat.typing $+(%key,$chr(7),$nick)
  if ($nick == $me) && ($chat.echoed(%key,%id,$2-)) {
    if (%key == %chat.cur) chat.draw
    return
  }
  chat.add %key $nick $1 $iif(%id,%id,-) $iif(%reply,%reply,-) $2-
  chat.draw
}

; Tag-only messages carry typing notices and reactions.
raw TAGMSG:*:{
  var %key = $iif($left($1,1) isin $chantypes,$chat.key($cid,$1),$chat.key($cid,$nick))
  if ($nick == $me) return
  var %typing = $msgtags(+typing)
  if (%typing == active) hadd chat.typing $+(%key,$chr(7),$nick) $ctime
  elseif (%typing != $null) hdel chat.typing $+(%key,$chr(7),$nick)
  var %emoji = $msgtags(+draft/react), %undo = $msgtags(+draft/unreact), %reply = $msgtags(+draft/reply)
  if (%reply != $null) && ((%emoji != $null) || (%undo != $null)) {
    var %src = $chat.byid(%reply)
    if (%src != $null) chat.react $gettok(%src,1,32) $gettok(%src,2,32) $nick $iif(%emoji,%emoji,%undo) $iif(%undo,1)
  }
  if (%key == %chat.cur) chat.draw
}
