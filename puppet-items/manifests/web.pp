class wordpress::web {

  # Install Apache
  class {'apache':
    mpm_module => 'prefork'
  }

  # Add support for PHP
  class {'::apache::mod::php': }
}
