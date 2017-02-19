#! /usr/bin/ruby

#puts '<?xml version="1.0" encoding="UTF-8"?><gpx version="1.0">'
#puts '<name>Trip</name>'
#puts '<trk><trkseg>'

puts 'type,time,latitude,longitude,speed,rssi'

while gets do
  if $_ =~ / (\S+)dBm/ then
    rssi = $1
  elsif $_ =~ /gpsNav (2017\S+) ([^.]+)\S+ OK <(\S+) ([^>]+)> ([^k]+)/ then
    puts "T,#{$1}T#{$2}Z,#{$3},#{$4},#{$5},#{rssi}"
    #puts %Q{<trkpt lat="#{$3}" lon="#{$4}"><time>#{$1}T#{$2}Z</time></trkpt>}
  end
end

#puts '</trkseg></trk>'
