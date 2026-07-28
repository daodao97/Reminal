class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "1.9.0"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.0/reminal_1.9.0_darwin_arm64.tar.gz"
      sha256 "8e8c594844b2bfda2d02d233bd6d695e91ce9d2ffb07e20f728e75bf6147e679"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.0/reminal_1.9.0_darwin_amd64.tar.gz"
      sha256 "2bef1dd442d1f4eccbf514db1334eeb13869bf27d149bcc7877bea468daa8fb4"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.0/reminal_1.9.0_linux_arm64.tar.gz"
      sha256 "9d5015c95a9081c8b015147e7f6a8f647522e4df10490a57ba14a14df1f70dd1"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.0/reminal_1.9.0_linux_amd64.tar.gz"
      sha256 "59ec55572fb3d5e3ac8d42a133e5a3b913c5f5536da49fcbbdf952f3f94488db"
    end
  end

  depends_on "go" => :build if build.head?

  def install
    if build.head?
      system "go", "build", "-ldflags=#{ldflags}", "-o", bin/"reminal", "./cmd/reminal"
    else
      bin.install "reminal"
    end
  end

  def ldflags
    "-s -w " \
      "-X main.version=#{version} " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudRelay=wss://reminal-relay.futuristic.workers.dev/ws " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudWeb=https://reminal-relay.futuristic.workers.dev"
  end

  def caveats
    <<~EOS
      reminal connects to the hosted relay automatically — no setup needed.

        reminal              # share your terminal
        reminal --connect ID --pin PIN
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/reminal version")
  end
end
