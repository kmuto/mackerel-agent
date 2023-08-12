#!/usr/bin/env ruby
require 'time'

array = []
ARGF.each do |l|
  next if l.match?('START')
  v = l.chomp.split(/\s*:\s*/)
  # v[1]: 動作時刻
  # v[2]: 作業内容
  # v[3]: メトリック投稿時刻
  # v[5]: キュー長

  array << {
    t: v[1].to_i,
    w: v[2],
    ms: v[3], # 2つ入っていることがあるのでまだいじらない
    q: v[5]
  }
  if v[3].nil?
    raise l
  end
end

lost = []
remain = []
created = []

sorted_array = array.sort {|a, b| a[:t] <=> b[:t] }
sorted_array.each do |v|
  s = ''
  # メトリックの値は常に0分のものとしているので秒は省略
  if v[:ms].include?(' ')
    s = v[:ms].split(' ').map do |v2|
      Time.at(v2.to_i).strftime('%H:%M')
    end.join(' ')
  else
    s = Time.at(v[:ms].to_i).strftime('%H:%M')
    created.push(s)
  end
  if v[:q]
    puts %Q(#{Time.at(v[:t]).strftime('%H:%M:%S')},#{v[:w]}[#{v[:q]}],#{s})
  else
    puts %Q(#{Time.at(v[:t]).strftime('%H:%M:%S')},#{v[:w]},#{s})
  end
  if v[:w] == 'LOST'
    lost << s
  end
  if v[:w] == 'REMAIN'
    remain << s
  end
end

if lost.size > 0
  puts '-----'
  puts 'LOST:'
  puts lost.sort.join("\n")
end

if remain.size > 0
  puts '-----'
  puts 'REMAIN:'
  puts remain.sort.join("\n")
end

prevtime = nil
errs = []
created.sort.uniq.each do |t|
  if prevtime
    now = Time.parse(t)
    if now != (prevtime + 60)
      errs << t
    end
  end
  prevtime = now
end
unless errs.empty?
  puts '-----'
  puts 'MISS:'
  puts errs.join("\n")
end
