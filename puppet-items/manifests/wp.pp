class wordpress::wp {

  #Copy wordpress bundle to /tmp
  file{ '/tmp/latest.tar.gz':
    ensure => present,
    source => "puppet:///modules/wordpress/latest.tar.gz"
  }

  #Extract wordpress bundle
  exec { 'extract':
    cwd => "/tmp",
    command => "tar -xvzf latest.tar.gz",
    require => File['/tmp/latest.tar.gz'],
    path => ['/bin'],
  }

  #Copy to /var/www
  exec { 'copy':
    command => "cp -r /tmp/wordpress/* /var/www/",
    require => Exec['extract'],
    path => ['/bin'],
  }

  #Generate wp-config.php file using a template
  file { '/var/www/wp-config.php':
    ensure => present,
    require => Exec['copy'],
    content => template("wordpress/wp-config.php.erb")
  }

}
