class wordpress::wp {

  package { 'wget':
    ensure => 'installed',
  }

  exec { 'download-wordpress':
    cwd => '/tmp',
    path => ['/bin', '/usr/bin'],
    command => 'wget -q https://wordpress.org/latest.tar.gz',
    require => Package['wget']
  }

  #Extract wordpress bundle
  exec { 'extract':
    cwd => "/tmp",
    command => "tar -xvzf latest.tar.gz",
    require => Exec['download-wordpress'],
    path => ['/bin'],
  }

  #Copy to /var/www/html
  exec { 'copy':
    command => "cp -r /tmp/wordpress/* /var/www/html",
    require => Exec['extract'],
    path => ['/bin'],
    notify => Exec['cleanup']
  }

  exec { 'cleanup':
    command => "rm -r /tmp/wordpress /tmp/latest.tar.gz /var/www/html/index.html || true",
    path => ['/bin'],
    subscribe => Exec['copy'],
  }

  #Generate wp-config.php file using a template
  file { '/var/www/html/wp-config.php':
    ensure => present,
    require => Exec['copy'],
    content => template("wordpress/wp-config.php.erb")
  }

}
